# frozen_string_literal: true

require_relative "test_helper"
require "json"
require "open3"
require "tasks/check"
require "tasks/format"

# End-to-end CLI coverage for the lead-time surface: `tasks lead`, `capture
# --lead`, the rendering `show`/`list` add, and the refusals rules 1-5 produce
# on this transport. The canonical model is proven in test_lead.rb; this file
# proves the CLI projects it faithfully.
class TestCliLead < Minitest::Test
  BIN = File.expand_path("../bin/tasks", __dir__)

  # A one-section sandbox, so a capture's project is unambiguous.
  RECORDS = [
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "dddd0001", "title" => "Work" },
    { "type" => "task", "id" => "dddd0002", "parent" => "dddd0001", "state" => "TODO",
      "title" => "Renew the passport", "deadline" => "2026-11-01" },
    { "type" => "task", "id" => "dddd0003", "parent" => "dddd0001", "state" => "TODO",
      "title" => "File quarterly sales tax", "scheduled" => "2026-04-20", "recur" => "+3m" },
    { "type" => "task", "id" => "dddd0004", "parent" => "dddd0001", "state" => "TODO",
      "title" => "Undated errand" },
  ].freeze

  # -- the motivating captures ----------------------------------------------

  def test_deadline_anchored_capture_hides_until_the_derived_date
    in_sandbox(today: "2026-10-01") do |org, run|
      out, _err, status = run.call("capture", "Renew the license", "--due", "2026-11-01",
                                   "--lead", "3w", "--project", "Work")
      assert status.success?, out
      record = record_for(org, title: "Renew the license")
      assert_equal "3w", record["lead"]

      listed, = run.call("list")
      refute_match(/Renew the license/, listed, "hidden before the window opens")

      unavailable, = run.call("list", "--unavailable")
      assert_match(/Renew the license/, unavailable)
      # The review shows the derived date AND the intent behind it.
      assert_match(%r{unavailable until 2026-10-11 · 3w before 11/1}, unavailable)
    end

    in_sandbox(today: "2026-10-11") do |_org, run|
      run.call("capture", "Renew the license", "--due", "2026-11-01", "--lead", "3w",
               "--project", "Work")
      listed, = run.call("list")
      assert_match(/Renew the license/, listed, "visible from the derived date on")
    end
  end

  def test_recurring_scheduled_capture_keeps_its_window_across_a_roll
    in_sandbox(today: "2026-04-14") do |org, run|
      out, _err, status = run.call("capture", "File quarterly VAT", "--scheduled",
                                   "2026-04-20", "--recur", "every 3 months on the 20th",
                                   "--lead", "1w", "--project", "Work")
      assert status.success?, out

      listed, = run.call("list")
      assert_match(/File quarterly VAT/, listed, "inside the window")

      done_out, _err, done_status = run.call("done", "quarterly VAT")
      assert done_status.success?, done_out
      record = record_for(org, title: "File quarterly VAT")
      assert_equal "2026-07-20", record["scheduled"], "the recurrence rolled"
      assert_equal "1w", record["lead"], "the window survives the roll"

      after, = run.call("list")
      refute_match(/File quarterly VAT/, after, "hidden again for the rest of the cycle")
    end
  end

  # -- tasks lead ------------------------------------------------------------

  def test_lead_sets_the_window_and_reports_the_resulting_availability
    in_sandbox(today: "2026-07-01") do |org, run|
      out, _err, status = run.call("lead", "passport", "3w")
      assert status.success?
      assert_match(/lead time 3w on "Renew the passport" \(3w before 2026-11-01\)/, out)
      assert_match(/unavailable until 2026-10-11/, out)
      assert_equal "3w", record_for(org, title: "Renew the passport")["lead"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_lead_accepts_phrases_and_off
    in_sandbox(today: "2026-07-01") do |org, run|
      run.call("lead", "passport", "a week")
      assert_equal "1w", record_for(org, title: "Renew the passport")["lead"]

      out, = run.call("lead", "passport", "off")
      assert_match(/clear the lead time/, out)
      assert_match(/available now/, out)
      refute record_for(org, title: "Renew the passport").key?("lead")
    end
  end

  def test_lead_read_only_preview_reports_the_span_and_the_opening_date
    in_sandbox(today: "2026-07-01") do |_org, run|
      run.call("lead", "passport", "3w")

      out, _err, status = run.call("lead", "passport")
      assert status.success?
      assert_match(/3 weeks before/, out)
      assert_match(/opens 2026-10-11/, out)

      json, = run.call("lead", "passport", "--json")
      row = JSON.parse(json)
      assert_equal "3w", row["lead"]
      assert_equal "3 weeks", row["lead_human"]
      assert_equal "2026-11-01", row["anchor"]
      assert_equal "2026-10-11", row["opens"]
    end
  end

  def test_lead_dry_run_previews_without_writing
    in_sandbox(today: "2026-07-01") do |org, run|
      out, _err, status = run.call("lead", "passport", "3w", "--dry-run")
      assert status.success?
      assert_match(/would lead time 3w/, out)
      assert_match(/unavailable until 2026-10-11/, out)
      refute record_for(org, title: "Renew the passport").key?("lead")
    end
  end

  def test_show_and_json_carry_the_span_and_the_derived_date
    in_sandbox(today: "2026-07-01") do |_org, run|
      run.call("lead", "passport", "3w")

      out, = run.call("show", "passport")
      assert_match(/lead:\s+3w/, out)
      assert_match(/opens:\s+2026-10-11/, out)
      assert_match(/availability: unavailable until 2026-10-11/, out)

      json, = run.call("show", "passport", "--json")
      row = JSON.parse(json)
      assert_equal "3w", row["lead"]
      assert_equal "3 weeks", row["lead_human"]
      assert_equal false, row["available"]
      assert_equal "scheduled", row["availability_reason"]
      assert_match(/\A2026-10-11T/, row["available_at"])
    end
  end

  def test_undo_restores_byte_identical_records
    in_sandbox(today: "2026-07-01") do |org, run|
      before = File.read(org)
      run.call("lead", "passport", "3w")
      refute_equal before, File.read(org)

      _out, err, status = run.call("undo")
      assert status.success?, err
      assert_equal before, File.read(org)
    end
  end

  # -- refusals (rules 1-5) --------------------------------------------------

  def test_rule_one_refuses_a_lead_on_an_undated_task
    in_sandbox(today: "2026-07-01") do |_org, run|
      out, err, status = run.call("lead", "Undated errand", "3w")
      refute status.success?
      assert_match(/has no date to hide before/, out + err)
      assert_match(/tasks due/, out + err)
    end
  end

  # A lead rides with the dates, so `propose --lead` works on the same terms as
  # `propose --due` — the contract's wording, and the reason there is no
  # state rule.
  def test_a_proposal_can_carry_a_lead
    in_sandbox(today: "2026-07-01") do |org, run|
      out, err, status = run.call("propose", "Maybe renew the lease", "--due", "2026-11-01",
                                  "--lead", "3w", "--project", "Work")
      assert status.success?, out + err
      record = record_for(org, title: "Maybe renew the lease")
      assert_equal "PROPOSED", record["state"]
      assert_equal "3w", record["lead"]
      assert Tasks::Check.check(org).ok?

      # …and `lead` resolves it, like the other fields a proposal carries.
      preview, _preview_err, preview_status = run.call("lead", "renew the lease")
      assert preview_status.success?, preview
      assert_match(/opens 2026-10-11/, preview)
    end
  end

  def test_rule_three_refuses_a_second_timed_gate_and_a_defer_date
    in_sandbox(today: "2026-07-01") do |_org, run|
      run.call("schedule", "passport", "2026-10-01")
      out, err, status = run.call("lead", "passport", "3w")
      refute status.success?
      assert_match(/second, ignored gate/, out + err)
    end

    in_sandbox(today: "2026-07-01") do |_org, run|
      run.call("lead", "passport", "3w")
      out, err, status = run.call("defer", "passport", "2026-09-01")
      refute status.success?
      assert_match(/already hides until 3 weeks before/, out + err)
      assert_match(/tasks lead/, out + err)
    end
  end

  def test_rule_four_refuses_junk_and_accepts_a_clock_lead
    in_sandbox(today: "2026-07-01") do |org, run|
      _out, err, status = run.call("lead", "passport", "soonish")
      refute status.success?
      assert_match(/unrecognized lead time/, err)
      assert_match(/try: 3w/, err)

      hour_out, _hour_err, hour_status = run.call("lead", "passport", "5h")
      assert hour_status.success?, hour_out
      assert_equal "5h", record_for(org, title: "Renew the passport")["lead"]
      # An all-day anchor resolves to the first instant of its date, so 5h
      # before 2026-11-01 is 19:00 on 2026-10-31 local.
      assert_match(/2026-10-31 19:00/, hour_out)
    end
  end

  def test_rule_five_refuses_a_lead_outside_the_storable_range
    in_sandbox(today: "2026-07-01") do |_org, run|
      out, err, status = run.call("lead", "passport", "9999y")
      refute status.success?
      assert_match(/four-digit years/, out + err)
    end
  end

  def test_capture_refuses_a_lead_with_no_date_to_hide_before
    in_sandbox(today: "2026-07-01") do |_org, run|
      _out, err, status = run.call("capture", "No date", "--lead", "3w", "--project", "Work")
      refute status.success?
      assert_match(/needs a date to hide before/, err)

      # …including on a proposal, where the refusal is about the missing date,
      # not about the state.
      _p_out, p_err, p_status = run.call("propose", "Idea", "--lead", "3w", "--project", "Work")
      refute p_status.success?
      assert_match(/needs a date to hide before/, p_err)
    end
  end

  # The contract's two motivating captures — only `--project` added, because
  # this sandbox has no Inbox section for a bare capture to land in. A
  # recurring capture with no date normally starts repeating today, which with a
  # lead would put the window in the past; the first occurrence is seeded
  # instead, which is the whole claim the feature is named for.
  def test_the_contracts_motivating_captures_work_exactly
    in_sandbox(today: "2026-05-01") do |org, run|
      out, err, status = run.call("capture", "water succulents", "--recur", "2w:sun", "--lead", "1d",
                                  "--project", "Work")
      assert status.success?, out + err
      gutters, gutters_err, gutters_status =
        run.call("capture", "clean gutters", "--recur", "y:06-01", "--lead", "17d", "--project", "Work")
      assert gutters_status.success?, gutters + gutters_err

      assert_equal "2026-05-03", record_for(org, title: "water succulents")["scheduled"],
                   "anchored on the schedule's first occurrence, not today"
      assert_equal "2026-06-01", record_for(org, title: "clean gutters")["scheduled"]

      listed, = run.call("list")
      refute_match(/water succulents/, listed, "invisible until the Saturday before")
      refute_match(/clean gutters/, listed, "invisible until May 15")

      hidden, = run.call("list", "--unavailable")
      assert_match(%r{water succulents.*unavailable until 2026-05-02}, hidden)
      assert_match(%r{clean gutters.*unavailable until 2026-05-15}, hidden)
      assert Tasks::Check.check(org).ok?
    end

    # The Saturday before the succulents occurrence, and 17 days before June 1.
    in_sandbox(today: "2026-05-02") do |_org, run|
      run.call("capture", "water succulents", "--recur", "2w:sun", "--lead", "1d",
                                  "--project", "Work")
      assert_match(/water succulents/, run.call("list").first)
    end

    in_sandbox(today: "2026-05-15") do |_org, run|
      run.call("capture", "clean gutters", "--recur", "y:06-01", "--lead", "17d",
                                  "--project", "Work")
      assert_match(/clean gutters/, run.call("list").first)
    end
  end

  # Rule 5: a lead is an intent about a date and cannot outlive the last one.
  def test_clearing_the_last_date_clears_the_lead
    in_sandbox(today: "2026-07-01") do |org, run|
      run.call("lead", "passport", "3w")
      out, err, status = run.call("undate", "passport")
      assert status.success?, out + err
      record = record_for(org, title: "Renew the passport")
      refute record.key?("lead")
      refute record.key?("deadline")
      assert Tasks::Check.check(org).ok?
    end
  end

  # Rule 3 lands in the store for `schedule`, so the CLI has to surface the
  # store's own sentence rather than a generic "failed to set scheduled".
  def test_schedule_against_a_deadline_anchored_lead_task_reports_the_rule
    in_sandbox(today: "2026-07-01") do |_org, run|
      run.call("lead", "passport", "3w")
      out, err, status = run.call("schedule", "passport", "2026-10-01")
      refute status.success?
      assert_match(/second, ignored gate/, out + err)
      refute_match(/failed to set/, out + err)
    end
  end

  # -- activation ------------------------------------------------------------

  def test_activate_releases_one_occurrence_and_the_roll_re_arms_the_window
    in_sandbox(today: "2026-04-01") do |org, run|
      run.call("lead", "quarterly", "1w")
      hidden, = run.call("list")
      refute_match(/quarterly sales tax/, hidden)

      out, = run.call("activate", "quarterly")
      assert_match(/available now/, out)
      record = record_for(org, title: "File quarterly sales tax")
      assert_equal "2026-04-20", record["scheduled"], "the occurrence date survives activation"
      assert_equal "2026-04-20", record["lead_skip"]

      visible, = run.call("list")
      assert_match(/quarterly sales tax/, visible)

      run.call("done", "quarterly")
      rolled = record_for(org, title: "File quarterly sales tax")
      assert_equal "2026-07-20", rolled["scheduled"]
      refute rolled.key?("lead_skip"), "the release expires with its occurrence"

      after, = run.call("list")
      refute_match(/quarterly sales tax/, after, "the window re-armed"
                  )
      assert Tasks::Check.check(org).ok?
    end
  end

  private

  # A sandbox store plus a runner that pins `today` for every invocation, so
  # window boundaries are asserted against a fixed clock rather than the
  # developer's calendar.
  def in_sandbox(today:, records: RECORDS)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, Tasks::Format.dump(records))
      support = File.expand_path("support/sequenced_today.rb", __dir__)
      env = {
        "TASKS_FILE" => org, "TASKS_ARCHIVE" => archive,
        "XDG_CONFIG_HOME" => File.join(dir, "xdg"),
        "XDG_STATE_HOME" => File.join(dir, "state"),
        "TZ" => "America/Denver",
        "RUBYOPT" => "-r#{support}", "TASKS_TEST_TODAY_SEQUENCE" => today,
      }
      run = lambda do |*args|
        out, err, status = Open3.capture3(env, "ruby", BIN, *args)
        [out.force_encoding("UTF-8"), err.force_encoding("UTF-8"), status]
      end
      yield org, run
    end
  end
end
