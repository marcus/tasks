# frozen_string_literal: true

require_relative "test_helper"
require "open3"

# Durability + concurrency coverage for the A1 store hardening:
#   - Tasks::Atomic.write replaces files whole (temp + rename), never torn
#   - the on-disk Tasks::Journal makes undo survive a fresh Store instance
#     and be shared between the CLI and TUI
#   - Store#with_lock serializes concurrent writers so no update is lost
class TestJournal < Minitest::Test
  # A store whose journal lives inside the sandbox dir, so a second Store over
  # the same org file shares that history (as the CLI and TUI would).
  def with_journal_store
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, FIXTURE)
      jdir = File.join(dir, "journal")
      build = ->() { Tasks::Store.new(org: org, archive: archive, journal_dir: jdir) }
      yield build, org, archive, jdir
    end
  end

  # -- Atomic.write ------------------------------------------------------------

  def test_atomic_write_replaces_contents
    Dir.mktmpdir do |dir|
      path = File.join(dir, "f.txt")
      File.write(path, "old")
      Tasks::Atomic.write(path, "new")
      assert_equal "new", File.read(path)
    end
  end

  def test_atomic_write_leaves_no_temp_files_behind
    Dir.mktmpdir do |dir|
      path = File.join(dir, "gtd.org")
      Tasks::Atomic.write(path, "hi\n")
      # only the target — no .gtd.org.<pid>.tmp turds
      assert_equal ["gtd.org"], Dir.children(dir)
    end
  end

  def test_atomic_write_swaps_the_inode
    Dir.mktmpdir do |dir|
      path = File.join(dir, "f.txt")
      File.write(path, "old")
      before = File.stat(path).ino
      Tasks::Atomic.write(path, "new")
      refute_equal before, File.stat(path).ino,
                   "rename-based write should install a fresh inode, not truncate in place"
    end
  end

  def test_atomic_write_preserves_permissions
    Dir.mktmpdir do |dir|
      path = File.join(dir, "gtd.org")
      File.write(path, "secret\n")
      File.chmod(0o600, path)
      Tasks::Atomic.write(path, "still secret\n")
      assert_equal 0o600, File.stat(path).mode & 0o777,
                   "a private (0600) file must not silently widen to the umask default"
    end
  end

  def test_atomic_write_follows_symlinks
    Dir.mktmpdir do |dir|
      real = File.join(dir, "real.org")
      link = File.join(dir, "gtd.org")
      File.write(real, "old\n")
      File.symlink(real, link)
      Tasks::Atomic.write(link, "new\n")
      assert File.symlink?(link), "writing through a symlink must not replace the link with a file"
      assert_equal "new\n", File.read(real), "the real file behind the symlink must be updated"
    end
  end

  def test_atomic_write_through_a_dangling_symlink_keeps_the_link
    Dir.mktmpdir do |dir|
      real = File.join(dir, "real.org")
      link = File.join(dir, "gtd.org")
      File.symlink(real, link) # target does not exist yet — a dangling link
      Tasks::Atomic.write(link, "materialized\n")
      assert File.symlink?(link), "a dangling symlink must be written through, not clobbered"
      assert_equal "materialized\n", File.read(real), "the intended target is created"
    end
  end

  def test_atomic_write_survives_a_filesystem_that_rejects_chmod
    Dir.mktmpdir do |dir|
      path = File.join(dir, "gtd.org")
      File.write(path, "old\n")
      # Simulate a mount (CIFS/exFAT/FUSE) that refuses chmod: the write must
      # still land rather than aborting.
      File.stub(:chmod, ->(*) { raise Errno::EPERM }) do
        Tasks::Atomic.write(path, "new\n")
      end
      assert_equal "new\n", File.read(path)
    end
  end

  def test_mutation_through_a_symlinked_org_keeps_the_link
    Dir.mktmpdir do |dir|
      real = File.join(dir, "real.org")
      link = File.join(dir, "gtd.org")
      File.write(real, FIXTURE_ORG)
      File.symlink(real, link)
      store = Tasks::Store.new(org: link, archive: File.join(dir, "archive.org"),
                               journal_dir: File.join(dir, "journal"))
      assert store.complete!(store.items.find { |i| i.title.include?("Book flight") })
      assert File.symlink?(link), "a Dropbox/dotfiles symlink setup must survive a mutation"
      assert_match(/DONE.*Book flight/, File.read(real))
    end
  end

  # -- journal persistence -----------------------------------------------------

  def test_undo_survives_a_new_store_instance
    with_journal_store do |build, org, _a|
      s1 = build.call
      before = File.read(org)
      s1.complete!(s1.items.find { |i| i.title.include?("Book flight") })
      refute_equal before, File.read(org)

      # A brand-new Store (a fresh process, in effect) can still undo it.
      s2 = build.call
      kind, label = s2.undo!
      assert_equal :ok, kind
      assert_includes label, "Book flight"
      assert_equal before, File.read(org)
    end
  end

  def test_redo_survives_a_new_store_instance
    with_journal_store do |build, org, _a|
      s1 = build.call
      s1.set_priority!(s1.items.find { |i| i.title.include?("Book flight") }, "C")
      after = File.read(org)
      s1.undo!

      s2 = build.call
      kind, = s2.redo!
      assert_equal :ok, kind
      assert_equal after, File.read(org)
    end
  end

  def test_two_stores_share_one_history
    with_journal_store do |build, org, _a|
      original = File.read(org)
      # One "process" makes an edit...
      build.call.set_priority!(build.call.items.find { |i| i.title.include?("Book flight") }, "B")
      # ...another undoes it.
      assert_equal :ok, build.call.undo!.first
      assert_equal original, File.read(org)
    end
  end

  def test_journal_persists_only_between_matching_org
    # A journal keyed to one org path must not replay onto a different file.
    with_journal_store do |build, _org, _a, jdir|
      build.call.set_priority!(build.call.items.find { |i| i.title.include?("Book flight") }, "C")
      Dir.mktmpdir do |other|
        o2 = File.join(other, "gtd.org")
        File.write(o2, FIXTURE_ORG)
        # Same journal dir, different org file: the stored history is ignored.
        s = Tasks::Store.new(org: o2, archive: File.join(other, "archive.org"), journal_dir: jdir)
        assert_equal [:empty], s.undo!
      end
    end
  end

  def test_capping_keeps_only_recent_history_across_instances
    with_journal_store do |build, _org, _a|
      55.times do |i|
        s = build.call
        s.set_priority!(s.items.find { |it| it.title.include?("Book flight") }, %w[A B C][i % 3])
      end
      s = build.call
      undone = 0
      undone += 1 while s.undo!.first == :ok
      assert_equal Tasks::Store::UNDO_LIMIT, undone
    end
  end

  def test_blobs_are_garbage_collected_when_history_is_capped
    with_journal_store do |build, _org, _a, jdir|
      60.times do |i|
        s = build.call
        s.set_priority!(s.items.find { |it| it.title.include?("Book flight") }, %w[A B C][i % 3])
      end
      blobs = Dir.children(File.join(jdir, "blobs")).size
      # Capped to UNDO_LIMIT+1 states; blobs dedupe, so the count is bounded and
      # nowhere near the 60 mutations we made.
      assert_operator blobs, :<=, Tasks::Store::UNDO_LIMIT + 2
    end
  end

  def test_shared_history_across_two_path_spellings
    # A symlink and its target are the same file; edits through one must be
    # undoable through the other (one canonical history, one lock).
    Dir.mktmpdir do |dir|
      real = File.join(dir, "real.org")
      link = File.join(dir, "gtd.org")
      File.write(real, FIXTURE_ORG)
      File.symlink(real, link)
      archive = File.join(dir, "archive.org")
      before = File.read(real)

      via_link = Tasks::Store.new(org: link, archive: archive)
      via_link.complete!(via_link.items.find { |i| i.title.include?("Book flight") })

      via_real = Tasks::Store.new(org: real, archive: archive)
      assert_equal :ok, via_real.undo!.first, "the other spelling shares the history"
      assert_equal before, File.read(real)
    end
  end

  # -- resilience --------------------------------------------------------------

  def test_corrupt_cursor_degrades_to_empty_not_crash
    with_journal_store do |build, _org, _a, jdir|
      build.call.set_priority!(build.call.items.find { |i| i.title.include?("Book flight") }, "C")
      idx = File.join(jdir, "index.json")
      data = JSON.parse(File.read(idx))
      data["cursor"] = 999 # out of range — a hand-edit or truncated write
      File.write(idx, JSON.generate(data))

      # undo must not raise; it treats the corrupt index as no history...
      assert_equal [:empty], build.call.undo!
      # ...and a fresh mutation must still succeed (not crash in record()).
      s = build.call
      assert s.set_priority!(s.items.find { |i| i.title.include?("Book flight") }, "B")
    end
  end

  def test_missing_blob_degrades_to_empty_not_crash
    with_journal_store do |build, _org, _a, jdir|
      build.call.set_priority!(build.call.items.find { |i| i.title.include?("Book flight") }, "C")
      # A referenced blob vanishes (e.g. a partial crash) — undo must not ENOENT.
      Dir.children(File.join(jdir, "blobs")).each { |b| File.delete(File.join(jdir, "blobs", b)) }
      assert_equal [:empty], build.call.undo!
    end
  end

  def test_noop_mutation_records_no_history
    with_journal_store do |build, _org, _a|
      s = build.call
      water = s.items.find { |i| i.title.include?("Water the plants") }
      assert s.set_priority!(water, "B") # one real, undoable step

      fresh = build.call
      w2 = fresh.items.find { |i| i.title.include?("Water the plants") }
      # It already has @home, so re-adding @home writes identical content —
      # a true no-op that must not create an undoable entry.
      assert fresh.set_tags!(w2, add: ["@home"])

      u = build.call
      assert_equal :ok, u.undo!.first, "the priority change is undoable"
      assert_equal [:empty], u.undo!, "the no-op tag recorded nothing"
    end
  end

  # -- CLI undo/redo end-to-end ------------------------------------------------

  BIN = File.expand_path("../bin/tasks", __dir__)

  # Run bin/tasks against a fixed org + state dir so successive invocations
  # share one history (the real cross-process scenario).
  def cli(dir, *args)
    env = { "TASKS_FILE" => File.join(dir, "tasks.jsonl"),
            "TASKS_ARCHIVE" => File.join(dir, "archive.jsonl"),
            "XDG_STATE_HOME" => File.join(dir, "state") }
    out, err, st = Open3.capture3(env, "ruby", BIN, *args)
    [out.force_encoding("UTF-8"), err.force_encoding("UTF-8"), st]
  end

  def test_cli_undo_reverts_a_prior_invocation
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, FIXTURE)
      before = File.read(org)

      _o, _e, st = cli(dir, "done", "Book flight")
      assert st.success?
      assert_match(/DONE.*Book flight/, File.read(org))

      out, _e, st = cli(dir, "undo")
      assert st.success?
      assert_match(/undid: state → DONE: Book flight/, out)
      assert_equal before, File.read(org), "a separate CLI run undoes the earlier edit"

      out, _e, st = cli(dir, "redo")
      assert st.success?
      assert_match(/redid:/, out)
      assert_match(/DONE.*Book flight/, File.read(org))
    end
  end

  def test_cli_undo_with_empty_history_fails_cleanly
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE)
      _o, err, st = cli(dir, "undo")
      refute st.success?
      assert_match(/nothing to undo/, err)
    end
  end

  def test_cli_undo_refuses_after_out_of_band_edit
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, FIXTURE)
      cli(dir, "priority", "Book flight", "C")
      # Claude (or the user) edits the file directly afterward.
      File.write(org, File.read(org) + "** TODO added out of band\n")

      _o, err, st = cli(dir, "undo")
      refute st.success?
      assert_match(/changed since that edit/, err)
      assert_match(/added out of band/, File.read(org), "refused undo must not clobber the file")
    end
  end

  # -- locking -----------------------------------------------------------------

  def test_concurrent_writers_do_not_lose_updates
    # Two processes each append a distinct capture to the same file at once.
    # Without the lock, one read-modify-write clobbers the other; with it, both
    # captures survive.
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, FIXTURE)
      state = File.join(dir, "state")
      bin = File.expand_path("../bin/tasks", __dir__)
      env = { "TASKS_FILE" => org, "TASKS_ARCHIVE" => archive, "XDG_STATE_HOME" => state }

      threads = 8.times.map do |i|
        Thread.new { Open3.capture3(env, "ruby", bin, "capture", "concurrent item #{i}") }
      end
      threads.each(&:join)

      content = File.read(org)
      8.times { |i| assert_match(/concurrent item #{i}/, content, "capture #{i} was lost to a race") }
      assert Tasks::Check.check(org).ok?, "file must stay structurally valid under concurrency"
    end
  end
end
