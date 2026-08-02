# frozen_string_literal: true

require_relative "test_helper"
require "open3"
require "rbconfig"
require "json"
require_relative "../porting/merge/lib/manifest_merge"

# porting/manifest.jsonl is the port's progress-of-record: agents flip
# `status` and fill evidence on their port/* branch while main independently
# gains whole new slice records as campaigns are seeded. A line-based git
# merge conflicts, and any wholesale --ours/--theirs resolution silently
# drops whichever side it discards. Porting::ManifestMerge (porting/merge/lib/
# manifest_merge.rb) is the id-keyed 3-way merge that fixes that; this covers
# the merge library directly and the installed git driver end to end.
class TestPortingManifestMerge < Minitest::Test
  INSTALL = File.expand_path("../porting/merge/install", __dir__)
  DRIVER = File.expand_path("../porting/merge/manifest-merge-driver", __dir__)

  def record(id, **fields)
    { "id" => id }.merge(fields.transform_keys(&:to_s))
  end

  def dump(records) = records.map { |r| "#{JSON.generate(r)}\n" }.join

  def merge(base, ours, theirs)
    Porting::ManifestMerge.merge(base_text: dump(base), ours_text: dump(ours), theirs_text: dump(theirs))
  end

  def ids_of(text) = text.each_line.map { |line| JSON.parse(line)["id"] }

  # (a) The real-world case: a stale 44-record branch manifest with one slice
  # advanced to `ported`, merged against a 144-record main. No record is lost
  # and the advancement survives.
  def test_stale_branch_advancement_merges_into_a_larger_main_with_no_loss
    manifest = File.readlines(File.expand_path("../porting/manifest.jsonl", __dir__))
    assert_operator manifest.length, :>=, 144, "fixture assumption: porting/manifest.jsonl has grown to 144+ records"

    stale = manifest.first(44).dup
    flipped = JSON.parse(stale[0])
    flipped_id = flipped["id"]
    flipped["status"] = "ported"
    stale[0] = "#{JSON.generate(flipped)}\n"

    result = Porting::ManifestMerge.merge(
      base_text: manifest.first(44).join,
      ours_text: manifest.join, # main: 144 records
      theirs_text: stale.join   # stale branch: 44 records, one advanced
    )

    assert result.ok?, result.error
    out_ids = ids_of(result.text)
    assert_equal 144, out_ids.length
    assert_equal manifest.length, out_ids.uniq.length
    merged_flipped = JSON.parse(result.text.each_line.find { |l| JSON.parse(l)["id"] == flipped_id })
    assert_equal "ported", merged_flipped["status"]
  end

  # (b) Both sides change the same field to different values: refuse, don't guess.
  def test_same_field_changed_differently_on_both_sides_refuses
    base = [record("slice-a", status: "not_started")]
    ours = [record("slice-a", status: "translating")]
    theirs = [record("slice-a", status: "conformance")]

    result = merge(base, ours, theirs)

    refute result.ok?
    assert_includes result.error, "slice-a"
    assert_includes result.error, "status"
    assert_includes result.error, "translating"
    assert_includes result.error, "conformance"
  end

  # `status` gets no bespoke rule: one side advancing away from `not_started`
  # while the other leaves it alone is an ordinary one-sided field change, and
  # resolves to the advanced value without being logged as a conflict.
  def test_status_advanced_on_one_side_and_untouched_on_the_other_takes_the_advance
    base = [record("slice-a", status: "not_started")]
    ours = [record("slice-a", status: "not_started")]
    theirs = [record("slice-a", status: "characterizing")]

    result = merge(base, ours, theirs)

    assert result.ok?, result.error
    assert_equal "characterizing", JSON.parse(result.text)["status"]
  end

  # (c) Disjoint new records from both sides union cleanly.
  def test_disjoint_additions_from_both_sides_union_without_loss
    base = [record("slice-a", status: "not_started")]
    ours = base + [record("slice-ours-new", status: "not_started")]
    theirs = base + [record("slice-theirs-new", status: "not_started")]

    result = merge(base, ours, theirs)

    assert result.ok?, result.error
    assert_equal %w[slice-a slice-ours-new slice-theirs-new], ids_of(result.text)
  end

  # (d) A deletion honored: the side that dropped a record wins when the other
  # side left that record untouched.
  def test_deletion_by_one_side_is_honored_when_the_other_side_left_it_unmodified
    base = [record("slice-a"), record("slice-b")]
    ours = [record("slice-a")] # deleted slice-b
    theirs = base.map(&:dup) # unmodified

    result = merge(base, ours, theirs)

    assert result.ok?, result.error
    assert_equal %w[slice-a], ids_of(result.text)
  end

  # A record deleted on one side is instead an edit-over-delete when the
  # surviving side actually changed it -- the same policy tasks.jsonl's own
  # merge uses (lib/tasks/jsonl_merge.rb), and the one made explicit for
  # Marcus in the task report: an edited record is kept rather than treated as
  # an automatic conflict.
  def test_deletion_by_one_side_yields_to_a_genuine_edit_on_the_other_side
    base = [record("slice-a", status: "not_started")]
    ours = [] # deleted
    theirs = [record("slice-a", status: "characterizing")] # edited

    result = merge(base, ours, theirs)

    assert result.ok?, result.error
    assert_equal ["slice-a"], ids_of(result.text)
    assert_equal "characterizing", JSON.parse(result.text)["status"]
  end

  def test_both_sides_deleting_the_same_record_drops_it
    base = [record("slice-a"), record("slice-b")]
    ours = [record("slice-a")]
    theirs = [record("slice-a")]

    result = merge(base, ours, theirs)

    assert result.ok?, result.error
    assert_equal %w[slice-a], ids_of(result.text)
  end

  def test_identical_edits_on_both_sides_are_not_a_conflict
    base = [record("slice-a", status: "not_started")]
    ours = [record("slice-a", status: "ported")]
    theirs = [record("slice-a", status: "ported")]

    result = merge(base, ours, theirs)

    assert result.ok?, result.error
    assert_equal "ported", JSON.parse(result.text)["status"]
  end

  def test_unmerged_records_are_preserved_byte_for_byte
    weird = "#{JSON.generate({ "id" => "slice-a", "status" => "not_started", "notes" => "spacing  preserved" })}\n"
    base = [weird]
    result = Porting::ManifestMerge.merge(base_text: base.join, ours_text: base.join, theirs_text: base.join)

    assert result.ok?, result.error
    assert_equal weird, result.text
  end

  def test_a_record_added_identically_on_both_sides_is_not_a_conflict
    base = []
    ours = [record("slice-new", status: "not_started")]
    theirs = [record("slice-new", status: "not_started")]

    result = merge(base, ours, theirs)

    assert result.ok?, result.error
    assert_equal %w[slice-new], ids_of(result.text)
  end

  def test_a_record_added_differently_on_both_sides_merges_fields
    base = []
    ours = [record("slice-new", status: "not_started", risk: "low")]
    theirs = [record("slice-new", status: "not_started", risk: "high")]

    result = merge(base, ours, theirs)

    refute result.ok?
    assert_includes result.error, "risk"
  end

  def test_refuses_invalid_json
    base = [record("slice-a")]
    result = Porting::ManifestMerge.merge(base_text: dump(base), ours_text: dump(base),
                                          theirs_text: "not json at all\n")

    refute result.ok?
    assert_includes result.error, "theirs"
  end

  def test_refuses_duplicate_ids_within_one_side
    base = [record("slice-a")]
    theirs = "#{JSON.generate(record("slice-a"))}\n#{JSON.generate(record("slice-a", status: "ported"))}\n"
    result = Porting::ManifestMerge.merge(base_text: dump(base), ours_text: dump(base), theirs_text: theirs)

    refute result.ok?
    assert_includes result.error, "duplicate id"
  end

  def test_refuses_a_record_missing_an_id
    theirs = "#{JSON.generate({ "status" => "not_started" })}\n"
    result = Porting::ManifestMerge.merge(base_text: "", ours_text: "", theirs_text: theirs)

    refute result.ok?
    assert_includes result.error, "no string `id`"
  end

  # --- Git installation and driver end-to-end -------------------------------

  def git(repo, *args)
    stdout, stderr, status = Open3.capture3("git", "-C", repo, *args)
    assert status.success?, "git #{args.join(" ")} failed: #{stderr}"
    stdout.strip
  end

  def write_manifest(repo, records)
    FileUtils.mkdir_p(File.join(repo, "porting"))
    File.write(File.join(repo, "porting", "manifest.jsonl"), dump(records))
  end

  def init_repo_with_driver
    repo = Dir.mktmpdir("porting-manifest-merge-test")
    git(repo, "init", "-q")
    git(repo, "config", "user.name", "Merge Test")
    git(repo, "config", "user.email", "merge-test@example.com")
    write_manifest(repo, [record("slice-a", status: "not_started")])
    git(repo, "add", "porting/manifest.jsonl")
    git(repo, "commit", "-q", "-m", "base")

    _stdout, stderr, status = Open3.capture3(RbConfig.ruby, INSTALL, repo)
    assert status.success?, stderr
    repo
  end

  def test_install_creates_gitattributes_and_git_config
    repo = init_repo_with_driver

    attributes = File.read(File.join(repo, ".gitattributes"))
    assert_includes attributes, "porting/manifest.jsonl merge=portingmanifest"
    assert_equal "porting manifest jsonl 3-way record merge",
                 git(repo, "config", "--get", "merge.portingmanifest.name")
    assert_equal "#{DRIVER} %O %A %B %P %L %X %Y",
                 git(repo, "config", "--get", "merge.portingmanifest.driver")

    attr_check, = Open3.capture3("git", "-C", repo, "check-attr", "merge", "--", "porting/manifest.jsonl")
    assert_includes attr_check, "merge: portingmanifest"
  ensure
    FileUtils.remove_entry(repo) if repo
  end

  def test_install_is_idempotent_and_does_not_duplicate_the_attributes_line
    repo = init_repo_with_driver
    _stdout, stderr, status = Open3.capture3(RbConfig.ruby, INSTALL, repo)
    assert status.success?, stderr

    attributes = File.read(File.join(repo, ".gitattributes"))
    assert_equal 1, attributes.scan("porting/manifest.jsonl merge=portingmanifest").length
  ensure
    FileUtils.remove_entry(repo) if repo
  end

  def test_real_git_merge_resolves_a_status_advance_against_a_grown_main
    repo = init_repo_with_driver
    git(repo, "add", ".gitattributes")
    git(repo, "commit", "-q", "-m", "attrs")
    primary = git(repo, "branch", "--show-current")

    git(repo, "switch", "-q", "-c", "theirs")
    write_manifest(repo, [record("slice-a", status: "ported")])
    git(repo, "commit", "-q", "-am", "theirs advances slice-a")

    git(repo, "switch", "-q", primary)
    write_manifest(repo, [record("slice-a", status: "not_started"), record("slice-b", status: "not_started")])
    git(repo, "commit", "-q", "-am", "ours adds slice-b")

    _stdout, merge_stderr, merge_status = Open3.capture3("git", "-C", repo, "merge", "--no-edit", "theirs")
    assert merge_status.success?, merge_stderr

    merged = File.readlines(File.join(repo, "porting", "manifest.jsonl")).map { |l| JSON.parse(l) }
    assert_equal %w[slice-a slice-b], merged.map { |r| r["id"] }
    assert_equal "ported", merged.first["status"]
    refute_includes File.read(File.join(repo, "porting", "manifest.jsonl")), "<<<<<<<"
  ensure
    FileUtils.remove_entry(repo) if repo
  end

  def test_real_git_merge_refuses_with_conflict_markers_on_a_genuine_conflict
    repo = init_repo_with_driver
    git(repo, "add", ".gitattributes")
    git(repo, "commit", "-q", "-m", "attrs")
    primary = git(repo, "branch", "--show-current")

    git(repo, "switch", "-q", "-c", "theirs")
    write_manifest(repo, [record("slice-a", status: "conformance")])
    git(repo, "commit", "-q", "-am", "theirs: conformance")

    git(repo, "switch", "-q", primary)
    write_manifest(repo, [record("slice-a", status: "translating")])
    git(repo, "commit", "-q", "-am", "ours: translating")

    _stdout, merge_stderr, merge_status = Open3.capture3("git", "-C", repo, "merge", "--no-edit", "theirs")
    porcelain, = Open3.capture3("git", "-C", repo, "status", "--porcelain", "porting/manifest.jsonl")
    conflicted = File.read(File.join(repo, "porting", "manifest.jsonl"))

    refute merge_status.success?, "git merge should fail: #{merge_stderr}"
    assert_includes porcelain, "UU porting/manifest.jsonl"
    assert_match(/^<{7} .*manifest merge failed: /, conflicted)
    assert_includes conflicted, "\n=======\n"
    assert_match(/^>{7} /, conflicted)
    assert_includes conflicted, "translating"
    assert_includes conflicted, "conformance"
  ensure
    FileUtils.remove_entry(repo) if repo
  end
end
