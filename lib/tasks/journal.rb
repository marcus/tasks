# frozen_string_literal: true

require "digest"
require "fileutils"
require "json"
require "securerandom"
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
  #   index.json    { version, org, cursor,
  #                   states: [{label?, org_sha, archive_sha, coalesce_key?, coalesce_scope?}] }
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
    CorruptBlob = Class.new(StandardError)
    private_constant :CorruptBlob

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

    def initialize(dir:, org:, limit:, coalesce_scope: nil)
      @dir = dir
      # Canonical (symlink-resolved) so the index's org guard matches no matter
      # how a sharing process spelled the path — same rationale as dir_for's key.
      @org = self.class.canonical(org)
      @limit = limit
      # Coalescing is deliberately local to one live Journal/Store instance.
      # Persisting this random scope on a keyed tip lets another instance share
      # undo history without accidentally extending the prior edit session.
      @coalesce_scope = (coalesce_scope || SecureRandom.hex(16)).to_s.dup.freeze
    end

    # Record a completed mutation. `before`/`after` are {org:, archive:} hashes
    # (a nil value means that file is absent at that state). Drops any redo tail,
    # appends or safely replaces the keyed tip, and caps history at `limit` undo
    # steps.
    #
    # If `before` doesn't match the recorded tip, an out-of-band edit slipped in
    # since the last record; the stale chain can no longer be safely replayed
    # (undoing across it would clobber that edit), so we discard it and start a
    # fresh baseline at `before`.
    # `repair: true` marks the produced state as the result of a targeted repair
    # of a previously-invalid record. It rides on the after-state so `plan` can
    # tell Store that undoing this step is *meant* to restore the malformed
    # `before` bytes — the one case where reverting to a Check-invalid state is
    # the user's intent rather than a hazard to gate.
    def record(label:, before:, after:, coalesce_key: nil, repair: false)
      ensure_directory(blobs_dir)
      idx = load
      tip_matches = !idx[:states].empty? &&
                    state_matches_snapshot?(idx[:states][idx[:cursor]], before)
      before_state = intern(before)
      at_tip = tip_matches && idx[:cursor] == idx[:states].length - 1
      tip = idx[:states][idx[:cursor]] if tip_matches
      key = coalesce_key if coalesce_key.is_a?(String)
      coalesce = key && at_tip && idx[:cursor].positive? && tip[:coalesce_key] == key &&
                 tip[:coalesce_scope] == @coalesce_scope &&
                 state_blobs_valid?(idx[:states][idx[:cursor] - 1])

      unless tip_matches
        states = [before_state]
        cursor = 0
      else
        states = idx[:states][0..idx[:cursor]]
        cursor = idx[:cursor]
      end

      state = intern(after).merge(label: label)
      state[:repair] = true if repair
      state.merge!(coalesce_key: key, coalesce_scope: @coalesce_scope) if key
      if coalesce
        # The repair flag marks a step whose BEFORE-state is invalid bytes, so
        # its undo is exempt from the restore-validity gate. A coalesced
        # follow-up edit replaces the step's content but keeps the same
        # before-state — the exemption must survive the overwrite or undoing
        # the coalesced step wrongly refuses to restore those bytes.
        state[:repair] = true if states[cursor][:repair]
        states[cursor] = state
      else
        states << state
        cursor += 1
      end

      if states.length > @limit + 1
        drop = states.length - (@limit + 1)
        states = states[drop..]
        cursor -= drop
      end
      persist(cursor, states, gc: true)
    rescue SystemCallError, IOError
      # History is convenience state. An unreadable or non-repairable journal
      # must not roll back a task mutation that was already durably written.
      false
    end

    # Plan an undo (delta -1) or redo (delta +1): the label of the mutation being
    # reverted/replayed, the Snapshot the live files must currently match
    # (`expect`), the Snapshot to move to (`target`), and a `commit` to persist
    # the cursor move once the caller has rewritten the files. nil when there's
    # no step in that direction. This and the commit run under the caller's held
    # lock, so no other process moves the cursor in between.
    #
    # Missing, unreadable, or non-regular journal files degrade to nil — nothing
    # to undo — rather than raising, upholding the "journal trouble never crashes
    # a command" contract. Fatal process errors are deliberately not contained.
    def plan(delta)
      idx = load
      from = idx[:cursor]
      to = from + delta
      return nil unless to.between?(0, idx[:states].length - 1)
      # The label lives on the higher-indexed of the two states — the mutation
      # that sits *between* them (undo reverts it, redo replays it).
      tip = idx[:states][[from, to].max]
      label = tip[:label]
      { label: label, repair: tip[:repair] == true,
        expect: content(idx[:states][from]), target: content(idx[:states][to]),
        commit: lambda {
          # Undo and redo are explicit history boundaries. Strip all segment
          # metadata so even a redo back to the exact former tip cannot resume
          # coalescing with an editor session that preceded the history move.
          states = idx[:states].map { |state| state.except(:coalesce_key, :coalesce_scope) }
          persist(to, states, gc: false)
        },
        rollback: lambda {
          # Atomic.write failures ordinarily leave the old index untouched. If
          # a wrapper/filesystem reports failure after installing the new index,
          # restore the captured cursor and state metadata exactly once.
          current = load
          persist(from, idx[:states], gc: false) unless current == idx
          true
        } }
    rescue SystemCallError, IOError, CorruptBlob
      nil
    end

    private

    def index_path = File.join(@dir, "index.json")
    def blobs_dir  = File.join(@dir, "blobs")
    def blob_path(sha) = File.join(blobs_dir, sha)

    def load
      return blank unless regular_file?(index_path)
      data = JSON.parse(File.read(index_path, encoding: "UTF-8"), symbolize_names: true)
      return blank unless data.is_a?(Hash)
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
      return blank unless states.all? { |state| valid_state?(state) }
      { cursor: cursor, states: states }
    rescue SystemCallError, IOError, JSON::ParserError
      blank
    end

    def blank = { cursor: 0, states: [] }

    def valid_state?(state)
      return false unless state.is_a?(Hash)
      return false unless %i[org_sha archive_sha].all? do |key|
        state[key].nil? ||
          (state[key].is_a?(String) && state[key].match?(/\A[0-9a-f]{64}\z/))
      end
      return false unless state[:label].nil? || state[:label].is_a?(String)
      return false unless state[:repair].nil? || state[:repair] == true || state[:repair] == false
      return false unless state[:coalesce_key].nil? || state[:coalesce_key].is_a?(String)
      state[:coalesce_scope].nil? || state[:coalesce_scope].is_a?(String)
    end

    def state_matches_snapshot?(state, snapshot)
      %i[org archive].all? do |kind|
        expected = snapshot[kind]
        sha = state[:"#{kind}_sha"]
        expected.nil? ? sha.nil? : sha && blob_bytes(sha) == expected.b
      end
    rescue SystemCallError, IOError, CorruptBlob
      false
    end

    def state_blobs_valid?(state)
      state && %i[org_sha archive_sha].all? do |key|
        state[key].nil? || Digest::SHA256.hexdigest(blob_bytes(state[key])) == state[key]
      end
    rescue SystemCallError, IOError, CorruptBlob
      false
    end

    # gc only when the state set may have shrunk (a `record` that capped or
    # dropped a redo tail) — an undo/redo commit leaves `states` untouched, so
    # scanning the blob dir for it would be pure waste on the hot path.
    def persist(cursor, states, gc:)
      ensure_directory(blobs_dir)
      data = { version: VERSION, org: @org, cursor: cursor,
               states: states.map { |s| compact(s) } }
      discard_nonregular(index_path)
      Atomic.write(index_path, JSON.pretty_generate(data))
      collect(states) if gc
    end

    def compact(state)
      h = { org_sha: state[:org_sha], archive_sha: state[:archive_sha] }
      h[:label] = state[:label] if state[:label]
      h[:repair] = true if state[:repair]
      h[:coalesce_key] = state[:coalesce_key] if state[:coalesce_key]
      h[:coalesce_scope] = state[:coalesce_scope] if state[:coalesce_scope]
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
      return sha if regular_blob_matches?(path, sha, text)
      # Repair a tampered/truncated blob before a new baseline references it.
      # Symlinks and other special files are unlinked, never followed; an empty
      # directory at the blob path is removed. A non-empty/unremovable entry is
      # left alone and record() degrades without disturbing the task write.
      discard_nonregular(path)
      Atomic.write(path, text)
      sha
    end

    def content(state)
      { org: read(state[:org_sha]), archive: read(state[:archive_sha]) }
    end

    def read(sha)
      return nil if sha.nil?
      bytes = blob_bytes(sha)
      raise CorruptBlob unless Digest::SHA256.hexdigest(bytes) == sha
      text = bytes.force_encoding(Encoding::UTF_8)
      raise CorruptBlob unless text.valid_encoding?
      text
    end

    def blob_bytes(sha)
      path = blob_path(sha)
      raise CorruptBlob unless regular_file?(path)
      File.binread(path)
    end

    def regular_blob_matches?(path, sha, text)
      return false unless regular_file?(path)
      bytes = File.binread(path)
      bytes == text.b && Digest::SHA256.hexdigest(bytes) == sha
    rescue SystemCallError, IOError
      false
    end

    def regular_file?(path)
      File.lstat(path).file?
    rescue SystemCallError
      false
    end

    def ensure_directory(path)
      stat = File.lstat(path)
      return if stat.directory?
      File.unlink(path)
      Dir.mkdir(path)
    rescue Errno::ENOENT
      FileUtils.mkdir_p(path)
    end

    def discard_nonregular(path)
      stat = File.lstat(path)
      return if stat.file?
      stat.directory? ? Dir.rmdir(path) : File.unlink(path)
    rescue Errno::ENOENT
      nil
    end

    # Delete blobs no live state references (freed by capping or a dropped redo
    # tail). Best-effort: a leaked blob wastes a little disk, never breaks undo.
    def collect(states)
      keep = Set.new(states.flat_map { |s| [s[:org_sha], s[:archive_sha]] }.compact)
      Dir.children(blobs_dir).each do |name|
        File.delete(blob_path(name)) unless keep.include?(name)
      end
    rescue SystemCallError, IOError
      # Missing/unreadable/non-regular blob state is already a history break.
      # Garbage collection is best-effort and must not affect task durability.
    end
  end
end
