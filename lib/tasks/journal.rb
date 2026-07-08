# frozen_string_literal: true

require "digest"
require "fileutils"
require "json"
require "set"
require_relative "atomic"
require_relative "config"

module Tasks
  # A durable, cross-process undo history for tasks.jsonl (and archive.jsonl).
  #
  # The in-memory undo stack the TUI used to keep died with the process and was
  # invisible to the CLI. This journal persists every mutation to disk, so
  # `tasks undo` works from a cold CLI invocation and the TUI's history survives
  # a restart — and, because both point at the same journal for the same org
  # file, they share one linear history.
  #
  # Layout (under XDG_STATE_HOME/tasks/journal/<key>/, keyed by the org's path):
  #   index.json    { version, org, cursor, states: [{label?, org_sha, archive_sha}] }
  #   blobs/<sha>   raw file contents, content-addressed and deduplicated
  #
  # `states` is the whole timeline; `cursor` indexes the state that matches the
  # live files. states[0] is the baseline (no label); each later state carries
  # the label of the mutation that produced it. `undo` walks the cursor back and
  # rewrites the files to states[cursor-1]; `redo` walks it forward. A mutation
  # made after an undo drops the now-unreachable tail (the familiar "a new edit
  # clears redo").
  #
  # States are content-addressed, so an ordinary edit writes just one new org
  # blob — the untouched archive deduplicates to the same sha — and a mutation
  # costs one small blob plus a tiny index rewrite, never a re-serialization of
  # the entire history.
  #
  # The journal is convenience state, not the source of truth: tasks.jsonl is. Every
  # method here runs under Store's file lock, and index.json is replaced
  # atomically, so concurrent processes never corrupt it — but a crash in the
  # narrow window between rewriting the files and committing the cursor can
  # leave the cursor one step stale. That degrades to a refused undo (a
  # conflict), never to a mangled task file.
  class Journal
    VERSION = 1

    # Journal directory for an org file. Uses XDG_STATE_HOME (the standard home
    # for persist-but-non-precious state), namespaced by a hash of the org's
    # canonical path so distinct task files never share a history — and, just as
    # importantly, so two spellings of the *same* file (a symlink, a relative
    # path) resolve to one history rather than silently diverging.
    def self.dir_for(org, env: ENV)
      base = Config.xdg_base("XDG_STATE_HOME", ".local", "state", env: env)
      key = Digest::SHA256.hexdigest(canonical(org))[0, 16]
      File.join(base, "tasks", "journal", key)
    end

    # Absolute, symlink-resolved path — the stable identity of a task file across
    # the different ways callers spell it. Falls back to a plain expansion when
    # the file doesn't exist yet (nothing to resolve).
    def self.canonical(org)
      File.realpath(org)
    rescue Errno::ENOENT
      # The file doesn't exist yet (first-run capture bootstraps it). Resolve the
      # containing directory — which does exist — so the identity stays stable
      # once the file appears: otherwise a /tmp → /private/tmp style symlink,
      # unresolved while the file is missing but resolved afterward, would shift
      # the journal key between the capture and its undo.
      dir = File.dirname(org)
      dir = File.realpath(dir) if File.exist?(dir)
      File.join(File.expand_path(dir), File.basename(org))
    end

    def initialize(dir:, org:, limit:)
      @dir = dir
      # Canonical (symlink-resolved) so the index's org guard matches no matter
      # how a sharing process spelled the path — same rationale as dir_for's key.
      @org = self.class.canonical(org)
      @limit = limit
    end

    # Record a completed mutation. `before`/`after` are {org:, archive:} hashes
    # (a nil value means that file is absent at that state). Drops any redo tail,
    # appends the new state, and caps history at `limit` undo steps.
    #
    # If `before` doesn't match the recorded tip, an out-of-band edit slipped in
    # since the last record; the stale chain can no longer be safely replayed
    # (undoing across it would clobber that edit), so we discard it and start a
    # fresh baseline at `before`.
    def record(label:, before:, after:)
      FileUtils.mkdir_p(blobs_dir)
      idx = load
      before_state = intern(before)
      if idx[:states].empty? ||
         idx[:states][idx[:cursor]].slice(:org_sha, :archive_sha) != before_state
        states = [before_state]
        cursor = 0
      else
        states = idx[:states][0..idx[:cursor]]
        cursor = idx[:cursor]
      end
      states << intern(after).merge(label: label)
      cursor += 1

      if states.length > @limit + 1
        drop = states.length - (@limit + 1)
        states = states[drop..]
        cursor -= drop
      end
      persist(cursor, states, gc: true)
    end

    # Plan an undo (delta -1) or redo (delta +1): the label of the mutation being
    # reverted/replayed, the Snapshot the live files must currently match
    # (`expect`), the Snapshot to move to (`target`), and a `commit` to persist
    # the cursor move once the caller has rewritten the files. nil when there's
    # no step in that direction. This and the commit run under the caller's held
    # lock, so no other process moves the cursor in between.
    #
    # ENOENT (a blob the index references was lost, e.g. to a truncated crash)
    # degrades to nil — nothing to undo — rather than raising, upholding the
    # "journal trouble never crashes a command" contract.
    def plan(delta)
      idx = load
      from = idx[:cursor]
      to = from + delta
      return nil unless to.between?(0, idx[:states].length - 1)
      # The label lives on the higher-indexed of the two states — the mutation
      # that sits *between* them (undo reverts it, redo replays it).
      label = idx[:states][[from, to].max][:label]
      { label: label, expect: content(idx[:states][from]), target: content(idx[:states][to]),
        commit: -> { persist(to, idx[:states], gc: false) } }
    rescue Errno::ENOENT
      nil
    end

    private

    def index_path = File.join(@dir, "index.json")
    def blobs_dir  = File.join(@dir, "blobs")
    def blob_path(sha) = File.join(blobs_dir, sha)

    def load
      data = JSON.parse(File.read(index_path, encoding: "UTF-8"), symbolize_names: true)
      # A key collision (different org, same 16-hex prefix) or a format bump
      # invalidates the whole history rather than replaying someone else's.
      return blank unless data[:version] == VERSION && data[:org] == @org
      states = data[:states]
      cursor = data[:cursor]
      # Never trust an out-of-range or non-integer cursor from a corrupted or
      # hand-edited index: indexing states with it would nil-deref and crash
      # every undo AND every subsequent mutation. Treat that as no history.
      return blank unless states.is_a?(Array) && cursor.is_a?(Integer) &&
                          cursor.between?(0, states.length - 1)
      { cursor: cursor, states: states }
    rescue Errno::ENOENT, JSON::ParserError
      blank
    end

    def blank = { cursor: 0, states: [] }

    # gc only when the state set may have shrunk (a `record` that capped or
    # dropped a redo tail) — an undo/redo commit leaves `states` untouched, so
    # scanning the blob dir for it would be pure waste on the hot path.
    def persist(cursor, states, gc:)
      FileUtils.mkdir_p(blobs_dir)
      data = { version: VERSION, org: @org, cursor: cursor,
               states: states.map { |s| compact(s) } }
      Atomic.write(index_path, JSON.pretty_generate(data))
      collect(states) if gc
    end

    def compact(state)
      h = { org_sha: state[:org_sha], archive_sha: state[:archive_sha] }
      h[:label] = state[:label] if state[:label]
      h
    end

    # Store both files' contents as blobs, returning their shas.
    def intern(snapshot)
      { org_sha: put(snapshot[:org]), archive_sha: put(snapshot[:archive]) }
    end

    def put(text)
      return nil if text.nil?
      sha = Digest::SHA256.hexdigest(text)
      path = blob_path(sha)
      # Content-addressed: identical content already on disk needs no rewrite.
      Atomic.write(path, text) unless File.exist?(path)
      sha
    end

    def content(state)
      { org: read(state[:org_sha]), archive: read(state[:archive_sha]) }
    end

    def read(sha)
      return nil if sha.nil?
      File.read(blob_path(sha), encoding: "UTF-8")
    end

    # Delete blobs no live state references (freed by capping or a dropped redo
    # tail). Best-effort: a leaked blob wastes a little disk, never breaks undo.
    def collect(states)
      keep = Set.new(states.flat_map { |s| [s[:org_sha], s[:archive_sha]] }.compact)
      Dir.children(blobs_dir).each do |name|
        File.delete(blob_path(name)) unless keep.include?(name)
      end
    rescue Errno::ENOENT
      # no blobs dir yet — nothing to collect
    end
  end
end
