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
      assert store.test_mutation.complete(store.items.find { |i| i.title.include?("Book flight") })
      assert File.symlink?(link), "a Dropbox/dotfiles symlink setup must survive a mutation"
      assert_match(/DONE.*Book flight/, File.read(real))
    end
  end

  # -- journal persistence -----------------------------------------------------

  def test_undo_survives_a_new_store_instance
    with_journal_store do |build, org, _a|
      s1 = build.call
      before = File.read(org)
      s1.test_mutation.complete(s1.items.find { |i| i.title.include?("Book flight") })
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
      s1.test_mutation.set_priority(s1.items.find { |i| i.title.include?("Book flight") }, "C")
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
      build.call.test_mutation.set_priority(build.call.items.find { |i| i.title.include?("Book flight") }, "B")
      # ...another undoes it.
      assert_equal :ok, build.call.undo!.first
      assert_equal original, File.read(org)
    end
  end

  def test_journal_persists_only_between_matching_org
    # A journal keyed to one org path must not replay onto a different file.
    with_journal_store do |build, _org, _a, jdir|
      build.call.test_mutation.set_priority(build.call.items.find { |i| i.title.include?("Book flight") }, "C")
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
        s.test_mutation.set_priority(s.items.find { |it| it.title.include?("Book flight") }, %w[A B C][i % 3])
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
        s.test_mutation.set_priority(s.items.find { |it| it.title.include?("Book flight") }, %w[A B C][i % 3])
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
      via_link.test_mutation.complete(via_link.items.find { |i| i.title.include?("Book flight") })

      via_real = Tasks::Store.new(org: real, archive: archive)
      assert_equal :ok, via_real.undo!.first, "the other spelling shares the history"
      assert_equal before, File.read(real)
    end
  end

  # -- resilience --------------------------------------------------------------

  def test_corrupt_cursor_degrades_to_empty_not_crash
    with_journal_store do |build, _org, _a, jdir|
      build.call.test_mutation.set_priority(build.call.items.find { |i| i.title.include?("Book flight") }, "C")
      idx = File.join(jdir, "index.json")
      data = JSON.parse(File.read(idx))
      data["cursor"] = 999 # out of range — a hand-edit or truncated write
      File.write(idx, JSON.generate(data))

      # undo must not raise; it treats the corrupt index as no history...
      assert_equal [:empty], build.call.undo!
      # ...and a fresh mutation must still succeed (not crash in record()).
      s = build.call
      assert s.test_mutation.set_priority(s.items.find { |i| i.title.include?("Book flight") }, "B")
    end
  end

  def test_corrupt_state_metadata_degrades_to_fresh_safe_history
    with_journal_store do |build, org, _a, jdir|
      first = build.call
      first.test_mutation.set_priority(first.items.find { |i| i.title.include?("Book flight") }, "C")
      before_second = File.read(org)
      idx = File.join(jdir, "index.json")
      data = JSON.parse(File.read(idx))
      data["states"].last["org_sha"] = "../../not-a-blob"
      File.write(idx, JSON.generate(data))

      second = build.call
      second.test_mutation.set_priority(second.items.find { |i| i.title.include?("Book flight") }, "B")
      assert_equal :ok, second.undo!.first
      assert_equal before_second, File.read(org)
      assert_equal [:empty], second.undo!
    end
  end

  def test_corrupt_top_level_index_shape_degrades_to_empty
    with_journal_store do |build, _org, _a, jdir|
      store = build.call
      store.test_mutation.set_priority(store.items.find { |i| i.title.include?("Book flight") }, "C")
      File.write(File.join(jdir, "index.json"), "[]")

      assert_equal [:empty], build.call.undo!
    end
  end

  def test_missing_blob_degrades_to_empty_not_crash
    with_journal_store do |build, _org, _a, jdir|
      build.call.test_mutation.set_priority(build.call.items.find { |i| i.title.include?("Book flight") }, "C")
      # A referenced blob vanishes (e.g. a partial crash) — undo must not ENOENT.
      Dir.children(File.join(jdir, "blobs")).each { |b| File.delete(File.join(jdir, "blobs", b)) }
      assert_equal [:empty], build.call.undo!
    end
  end

  def test_directory_at_current_blob_makes_undo_empty_and_next_record_repairs_history
    with_journal_store do |build, org, _a, jdir|
      first = build.call
      item = first.items.find { |i| i.title.include?("Book flight") }
      assert first.test_mutation.set_priority(item, "C")
      before_second = File.binread(org)
      blob = current_org_blob(jdir)
      File.delete(blob)
      Dir.mkdir(blob)

      assert_equal [:empty], first.undo!
      assert_equal before_second, File.binread(org), "failed inspection must not touch task bytes"

      second = build.call
      item = second.items.find { |i| i.title.include?("Book flight") }
      assert second.test_mutation.set_priority(item, "B")
      assert File.file?(blob), "the next record replaces the empty directory with a blob"
      assert_equal :ok, second.undo!.first
      assert_equal before_second, File.binread(org)
    end
  end

  def test_symlink_at_current_blob_is_never_followed_and_next_record_repairs_history
    with_journal_store do |build, org, _a, jdir|
      first = build.call
      item = first.items.find { |i| i.title.include?("Book flight") }
      assert first.test_mutation.set_priority(item, "C")
      before_second = File.binread(org)
      blob = current_org_blob(jdir)
      sentinel = File.join(File.dirname(jdir), "journal-symlink-target")
      File.write(sentinel, "do not read or replace me")
      File.delete(blob)
      File.symlink(sentinel, blob)

      assert_equal [:empty], first.undo!
      assert_equal before_second, File.binread(org)
      assert_equal "do not read or replace me", File.read(sentinel)

      second = build.call
      item = second.items.find { |i| i.title.include?("Book flight") }
      assert second.test_mutation.set_priority(item, "B")
      assert File.file?(blob)
      refute File.symlink?(blob)
      assert_equal "do not read or replace me", File.read(sentinel)
      assert_equal :ok, second.undo!.first
      assert_equal before_second, File.binread(org)
    end
  end

  def test_directory_at_current_blob_makes_redo_empty_without_touching_task_bytes
    with_journal_store do |build, org, _a, jdir|
      store = build.call
      item = store.items.find { |i| i.title.include?("Book flight") }
      assert store.test_mutation.set_priority(item, "C")
      assert_equal :ok, store.undo!.first
      before_redo = File.binread(org)
      blob = current_org_blob(jdir)
      File.delete(blob)
      Dir.mkdir(blob)

      assert_equal [:empty], store.redo!
      assert_equal before_redo, File.binread(org), "failed inspection must not touch task bytes"
    end
  end

  def test_directory_index_degrades_to_empty_and_next_record_rebuilds_history
    with_journal_store do |build, org, _a, jdir|
      first = build.call
      item = first.items.find { |i| i.title.include?("Book flight") }
      assert first.test_mutation.set_priority(item, "C")
      before_second = File.binread(org)
      index = File.join(jdir, "index.json")
      File.delete(index)
      Dir.mkdir(index)

      assert_equal [:empty], first.undo!
      assert_equal before_second, File.binread(org)

      second = build.call
      item = second.items.find { |i| i.title.include?("Book flight") }
      assert second.test_mutation.set_priority(item, "B")
      assert File.file?(index)
      assert_equal :ok, second.undo!.first
      assert_equal before_second, File.binread(org)
    end
  end

  def test_unreadable_blob_error_degrades_to_empty_but_fatal_errors_propagate
    with_journal_store do |build, org, _a, jdir|
      store = build.call
      item = store.items.find { |i| i.title.include?("Book flight") }
      assert store.test_mutation.set_priority(item, "C")
      live = File.binread(org)
      blob = current_org_blob(jdir)
      original = File.method(:binread)

      denied = lambda do |path, *args|
        raise Errno::EACCES, path if path == blob
        original.call(path, *args)
      end
      File.stub(:binread, denied) do
        assert_equal [:empty], store.undo!
      end
      assert_equal live, File.binread(org)

      fatal = lambda do |path, *args|
        raise NoMemoryError, "injected fatal" if path == blob
        original.call(path, *args)
      end
      File.stub(:binread, fatal) do
        assert_raises(NoMemoryError) { store.undo! }
      end
      assert_equal live, File.binread(org)
    end
  end

  def test_unreadable_index_error_degrades_to_empty_but_fatal_errors_propagate
    with_journal_store do |build, org, _a, jdir|
      store = build.call
      item = store.items.find { |i| i.title.include?("Book flight") }
      assert store.test_mutation.set_priority(item, "C")
      live = File.binread(org)
      index = File.join(jdir, "index.json")
      original = File.method(:read)

      denied = lambda do |path, *args, **kwargs|
        raise Errno::EACCES, path if path == index
        original.call(path, *args, **kwargs)
      end
      File.stub(:read, denied) do
        assert_equal [:empty], store.undo!
      end
      assert_equal live, File.binread(org)

      fatal = lambda do |path, *args, **kwargs|
        raise NoMemoryError, "injected fatal" if path == index
        original.call(path, *args, **kwargs)
      end
      File.stub(:read, fatal) do
        assert_raises(NoMemoryError) { store.undo! }
      end
      assert_equal live, File.binread(org)
    end
  end

  def test_undo_cursor_commit_failure_restores_org_archive_and_original_cursor
    with_journal_store do |build, org, archive, jdir|
      store = build.call
      assert_equal 1, store.archive_swept!
      before = { org: File.binread(org), archive: File.binread(archive) }
      cursor = journal_cursor(jdir)

      result = with_atomic_write_denied(File.join(jdir, "index.json")) { store.undo! }
      assert_equal [:conflict, "archive sweep"], result
      assert_equal before[:org], File.binread(org)
      assert File.exist?(archive)
      assert_equal before[:archive], File.binread(archive)
      assert_equal cursor, journal_cursor(jdir)

      assert_equal [:ok, "archive sweep"], store.undo!
      refute File.exist?(archive), "the retained cursor still points to the undoable sweep"
    end
  end

  def test_redo_cursor_commit_failure_restores_archive_absence_and_original_cursor
    with_journal_store do |build, org, archive, jdir|
      store = build.call
      assert_equal 1, store.archive_swept!
      swept = { org: File.binread(org), archive: File.binread(archive) }
      assert_equal [:ok, "archive sweep"], store.undo!
      before = File.binread(org)
      refute File.exist?(archive)
      cursor = journal_cursor(jdir)

      result = with_atomic_write_denied(File.join(jdir, "index.json")) { store.redo! }
      assert_equal [:conflict, "archive sweep"], result
      assert_equal before, File.binread(org)
      refute File.exist?(archive)
      assert_equal cursor, journal_cursor(jdir)

      assert_equal [:ok, "archive sweep"], store.redo!
      assert_equal swept[:org], File.binread(org)
      assert_equal swept[:archive], File.binread(archive)
    end
  end

  def test_org_restore_failure_keeps_files_and_cursor_at_original_state
    with_journal_store do |build, org, _archive, jdir|
      store = build.call
      item = store.items.find { |i| i.title.include?("Book flight") }
      assert store.test_mutation.set_priority(item, "C")
      before = File.binread(org)
      cursor = journal_cursor(jdir)

      result = with_atomic_write_denied(org) { store.undo! }
      assert_equal :conflict, result.first
      assert_equal before, File.binread(org)
      assert_equal cursor, journal_cursor(jdir)
      assert_equal :ok, store.undo!.first
    end
  end

  def test_archive_write_failure_during_redo_keeps_archive_absent
    with_journal_store do |build, org, archive, jdir|
      store = build.call
      assert_equal 1, store.archive_swept!
      assert_equal :ok, store.undo!.first
      before = File.binread(org)
      cursor = journal_cursor(jdir)

      result = with_atomic_write_denied(archive) { store.redo! }
      assert_equal [:conflict, "archive sweep"], result
      assert_equal before, File.binread(org)
      refute File.exist?(archive)
      assert_equal cursor, journal_cursor(jdir)
    end
  end

  def test_archive_delete_failure_during_undo_rolls_org_back_and_keeps_cursor
    with_journal_store do |build, org, archive, jdir|
      store = build.call
      assert_equal 1, store.archive_swept!
      before = { org: File.binread(org), archive: File.binread(archive) }
      cursor = journal_cursor(jdir)
      original = File.method(:delete)
      deleter = lambda do |path, *args|
        raise Errno::EACCES, path if path == archive
        original.call(path, *args)
      end

      result = File.stub(:delete, deleter) { store.undo! }
      assert_equal [:conflict, "archive sweep"], result
      assert_equal before[:org], File.binread(org)
      assert_equal before[:archive], File.binread(archive)
      assert_equal cursor, journal_cursor(jdir)
    end
  end

  def test_transient_rollback_write_failure_retries_without_split_state
    with_journal_store do |build, org, _archive, jdir|
      store = build.call
      item = store.items.find { |i| i.title.include?("Book flight") }
      assert store.test_mutation.set_priority(item, "C")
      before = File.binread(org)
      cursor = journal_cursor(jdir)
      index = File.join(jdir, "index.json")
      original = Tasks::Atomic.method(:write)
      org_writes = 0
      writer = lambda do |path, content|
        if path == org
          org_writes += 1
          raise Errno::EACCES, path if org_writes == 2
        end
        raise Errno::EACCES, path if path == index
        original.call(path, content)
      end

      result = Tasks::Atomic.stub(:write, writer) { store.undo! }
      assert_equal :conflict, result.first
      assert_equal 3, org_writes, "rollback retries once after its first write failure"
      assert_equal before, File.binread(org)
      assert_equal cursor, journal_cursor(jdir)
    end
  end

  def test_cursor_rollback_retries_when_commit_failed_after_installing_index
    with_journal_store do |build, org, _archive, jdir|
      store = build.call
      item = store.items.find { |i| i.title.include?("Book flight") }
      assert store.test_mutation.set_priority(item, "C")
      before = File.binread(org)
      cursor = journal_cursor(jdir)
      index = File.join(jdir, "index.json")
      original = Tasks::Atomic.method(:write)
      index_writes = 0
      writer = lambda do |path, content|
        if path == index
          index_writes += 1
          if index_writes == 1
            original.call(path, content)
            raise Errno::EACCES, path
          end
          raise Errno::EACCES, path if index_writes == 2
        end
        original.call(path, content)
      end

      result = Tasks::Atomic.stub(:write, writer) { store.undo! }
      assert_equal :conflict, result.first
      assert_equal 3, index_writes, "cursor rollback retries after its first write failure"
      assert_equal before, File.binread(org)
      assert_equal cursor, journal_cursor(jdir)
    end
  end

  def test_persistent_rollback_failure_keeps_archive_copy_and_original_cursor
    with_journal_store do |build, org, archive, jdir|
      store = build.call
      assert_equal 1, store.archive_swept!
      cursor = journal_cursor(jdir)
      original_write = Tasks::Atomic.method(:write)
      org_writes = 0
      writer = lambda do |path, content|
        if path == org
          org_writes += 1
          raise Errno::EACCES, path if org_writes > 1
        end
        original_write.call(path, content)
      end
      original_delete = File.method(:delete)
      deleter = lambda do |path, *args|
        raise Errno::EACCES, path if path == archive
        original_delete.call(path, *args)
      end

      result = Tasks::Atomic.stub(:write, writer) do
        File.stub(:delete, deleter) { store.undo! }
      end
      assert_equal [:conflict, "archive sweep"], result
      assert_equal 3, org_writes
      assert record_for(org, title: "Old finished thing"),
             "the forward install preserves a live copy when archive deletion fails"
      assert record_for(archive, title: "Old finished thing"),
             "the archive copy remains when rollback cannot restore the old live bytes"
      assert_equal cursor, journal_cursor(jdir)
      assert Tasks::Check.check(org).ok?
      assert Tasks::Check.check(archive).ok?
    end
  end

  def test_fatal_cursor_commit_error_rolls_files_back_then_propagates
    with_journal_store do |build, org, _archive, jdir|
      store = build.call
      item = store.items.find { |i| i.title.include?("Book flight") }
      assert store.test_mutation.set_priority(item, "C")
      before = File.binread(org)
      cursor = journal_cursor(jdir)
      index = File.join(jdir, "index.json")
      original = Tasks::Atomic.method(:write)
      writer = lambda do |path, content|
        raise NoMemoryError, "injected fatal" if path == index
        original.call(path, content)
      end

      assert_raises(NoMemoryError) do
        Tasks::Atomic.stub(:write, writer) { store.undo! }
      end
      assert_equal before, File.binread(org)
      assert_equal cursor, journal_cursor(jdir)
    end
  end

  def test_noop_mutation_records_no_history
    with_journal_store do |build, _org, _a|
      s = build.call
      water = s.items.find { |i| i.title.include?("Water the plants") }
      assert s.test_mutation.set_priority(water, "B") # one real, undoable step

      fresh = build.call
      w2 = fresh.items.find { |i| i.title.include?("Water the plants") }
      # It already has @home, so re-adding @home writes identical content —
      # a true no-op that must not create an undoable entry.
      assert fresh.test_mutation.set_tags(w2, add: ["@home"])

      u = build.call
      assert_equal :ok, u.undo!.first, "the priority change is undoable"
      assert_equal [:empty], u.undo!, "the no-op tag recorded nothing"
    end
  end

  def current_org_blob(jdir)
    data = JSON.parse(File.read(File.join(jdir, "index.json")))
    File.join(jdir, "blobs", data.fetch("states").fetch(data.fetch("cursor")).fetch("org_sha"))
  end

  def journal_cursor(jdir)
    JSON.parse(File.read(File.join(jdir, "index.json"))).fetch("cursor")
  end

  def with_atomic_write_denied(path)
    original = Tasks::Atomic.method(:write)
    writer = lambda do |candidate, content|
      raise Errno::EACCES, candidate if candidate == path
      original.call(candidate, content)
    end
    Tasks::Atomic.stub(:write, writer) { yield }
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

  # A coalesced follow-up edit replaces the repair step's state in place; the
  # repair flag must survive that overwrite, or undoing the coalesced step hits
  # the restore-validity gate and refuses to restore the invalid bytes.
  def test_coalescing_onto_a_repair_step_keeps_the_undo_exemption
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture([
        { "type" => "meta", "version" => 1 },
        { "type" => "section", "id" => "aaaa0001", "title" => "W" },
        { "type" => "task", "id" => "aaaa0002", "parent" => "aaaa0001", "state" => "TODO",
          "title" => "Fix me", "scheduled" => "not-a-date" },
      ]))
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl"),
                               journal_dir: File.join(dir, "journal"))
      refute Tasks::Check.check(org).ok?, "seed is invalid"
      invalid_bytes = File.read(org)

      repaired = store.patch_task!(Tasks::TaskPatch.new(
        id: "aaaa0002", field: :scheduled, value: Date.new(2026, 8, 1), expected: nil,
        coalesce_key: "edit-session"
      ))
      assert_equal :ok, repaired.status
      assert Tasks::Check.check(org).ok?, "repair leaves the file clean"

      followup = store.patch_task!(Tasks::TaskPatch.new(
        id: "aaaa0002", field: :scheduled, value: Date.new(2026, 8, 2),
        expected: Date.new(2026, 8, 1), coalesce_key: "edit-session"
      ))
      assert_equal :ok, followup.status

      status, = store.undo!
      assert_equal :ok, status, "undo of a coalesced repair step must not hit the validity gate"
      assert_equal invalid_bytes, File.read(org), "undo restores the exact pre-repair bytes"

      assert_equal :ok, store.redo!.first
      assert Tasks::Check.check(org).ok?, "redo re-applies the coalesced repair"
    end
  end
end
