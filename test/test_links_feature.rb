# frozen_string_literal: true

require_relative "test_helper"
require "open3"
require "json"
require "tui/app"

# Coverage for the links feature: config-driven shorthands (link.<name> rows),
# custom system hosts (system.<name> rows), the `tasks open` command, and the
# TUI `o` action + detail-panel links.
class TestLinksFeature < Minitest::Test
  # -- config parsing ----------------------------------------------------------

  def test_config_collects_link_and_system_rows
    Dir.mktmpdir do |dir|
      cfg = File.join(dir, "tasks", "config")
      FileUtils.mkdir_p(File.dirname(cfg))
      File.write(cfg, <<~CONF)
        dir = #{dir}
        link.jira = https://acme.atlassian.net/browse/%s
        link.gh = https://github.com/%s
        system.gitlab = gitlab.acme.io
        link.BAD NAME = https://x.co/%s
        system.= nope
      CONF
      paths = Tasks::Config.resolve(default_dir: dir, env: { "XDG_CONFIG_HOME" => dir })
      assert_equal %w[jira gh], paths.links.keys
      assert_equal "https://acme.atlassian.net/browse/%s", paths.links["jira"]
      assert_equal({ "gitlab" => "gitlab.acme.io" }, paths.link_systems)
    end
  end

  def test_config_for_dir_has_empty_link_maps
    paths = Tasks::Config.for_dir("/tmp/nowhere")
    assert_equal({}, paths.links)
    assert_equal({}, paths.link_systems)
  end

  # -- shorthand extraction ----------------------------------------------------

  SHORTHANDS = {
    "jira"  => "https://acme.atlassian.net/browse/%s",
    "gh"    => "https://github.com/%s",
    "slack" => "https://acme.slack.com/archives/%s",
  }.freeze

  def test_shorthand_expands_and_keeps_token_as_label
    links = Tasks::Links.extract("Ticket jira:OPS-1234, fix in gh:acme/app/pull/412.",
                                 shorthands: SHORTHANDS)
    assert_equal ["https://acme.atlassian.net/browse/OPS-1234",
                  "https://github.com/acme/app/pull/412"], links.map(&:url)
    assert_equal ["jira:OPS-1234", "gh:acme/app/pull/412"], links.map(&:label)
    assert_equal %w[jira github], links.map(&:system), "system comes from the expanded URL"
  end

  def test_shorthand_requires_configured_names
    links = Tasks::Links.extract("note: this is prose, and https:not-a-token either",
                                 shorthands: SHORTHANDS)
    assert_empty links
  end

  def test_shorthand_does_not_match_inside_urls
    # The "C042/p9" tail of a real Slack URL must not re-match as slack:…
    links = Tasks::Links.extract("see https://acme.slack.com/archives/C042/p9 now",
                                 shorthands: SHORTHANDS)
    assert_equal 1, links.size
    assert_nil links[0].label
  end

  def test_shorthand_template_without_percent_s_is_a_prefix
    links = Tasks::Links.extract("see t:ABC-9", shorthands: { "t" => "https://t.acme.io/" })
    assert_equal "https://t.acme.io/ABC-9", links[0].url
  end

  def test_shorthand_matches_after_brackets_and_quotes
    links = Tasks::Links.extract(%(see [jira:OPS-2] and "jira:OPS-3"), shorthands: SHORTHANDS)
    assert_equal ["https://acme.atlassian.net/browse/OPS-2",
                  "https://acme.atlassian.net/browse/OPS-3"], links.map(&:url)
  end

  def test_shorthand_value_keeps_balanced_brackets
    links = Tasks::Links.extract("see jira:PROJ[2] now", shorthands: SHORTHANDS)
    assert_equal "https://acme.atlassian.net/browse/PROJ[2]", links[0].url,
                 "a ] balancing an earlier [ belongs to the value, not the sentence"
  end

  def test_shorthand_inside_an_org_link_label_is_not_extracted
    # Pins a deliberate choice: an org link's label is display text — a
    # shorthand mentioned there does not become its own link.
    links = Tasks::Links.extract("[[https://x.io/a][see jira:OPS-5]]", shorthands: SHORTHANDS)
    assert_equal ["https://x.io/a"], links.map(&:url)
  end

  def test_config_strips_inline_comments_but_keeps_url_anchors
    Dir.mktmpdir do |dir|
      FileUtils.mkdir_p(File.join(dir, "tasks"))
      File.write(File.join(dir, "tasks", "config"), <<~CONF)
        link.jira = https://acme.atlassian.net/browse/%s   # notes can say jira:OPS-1
        link.doc = https://wiki.acme.io/page#s/%s
        system.gitlab = gitlab.acme.io  # self-hosted
      CONF
      paths = Tasks::Config.resolve(default_dir: dir, env: { "XDG_CONFIG_HOME" => dir })
      assert_equal "https://acme.atlassian.net/browse/%s", paths.links["jira"],
                   "the documented inline-comment style must parse"
      assert_equal "https://wiki.acme.io/page#s/%s", paths.links["doc"],
                   "a # inside a URL (no leading space) survives"
      assert_equal "gitlab.acme.io", paths.link_systems["gitlab"]
    end
  end

  def test_custom_system_rows_classify_self_hosted
    links = Tasks::Links.extract("https://gitlab.acme.io/g/p/-/merge_requests/4",
                                 systems: { "gitlab" => "gitlab.acme.io" })
    assert_equal "gitlab", links[0].system
    # subdomains of the custom host match too
    assert_equal "gitlab", Tasks::Links.classify("https://sub.gitlab.acme.io/x",
                                                 systems: { "gitlab" => "gitlab.acme.io" })
    # user rows win over the host fallback but not over unrelated hosts
    assert_equal "other.io", Tasks::Links.classify("https://other.io/x",
                                                   systems: { "gitlab" => "gitlab.acme.io" })
  end

  # -- CLI end-to-end ----------------------------------------------------------

  BIN = File.expand_path("../bin/tasks", __dir__)

  ORG_WITH_LINKS = dump_fixture([
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "cccc0001", "title" => "Work" },
    { "type" => "task", "id" => "cccc0002", "parent" => "cccc0001", "state" => "NEXT",
      "title" => "One-link task", "body" => "Ticket jira:OPS-7" },
    { "type" => "task", "id" => "cccc0003", "parent" => "cccc0001", "state" => "NEXT",
      "title" => "Many-link task", "body" => "jira:OPS-8 and https://acme.slack.com/archives/C1/p2" },
    { "type" => "task", "id" => "cccc0004", "parent" => "cccc0001", "state" => "NEXT",
      "title" => "Linkless task", "body" => "just prose" },
  ])

  # Sandbox with a config file carrying shorthand rows; XDG_CONFIG_HOME points
  # at it so the CLI resolves our config, never the developer's real one.
  def with_link_env
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), ORG_WITH_LINKS)
      FileUtils.mkdir_p(File.join(dir, "cfg", "tasks"))
      File.write(File.join(dir, "cfg", "tasks", "config"),
                 "link.jira = https://acme.atlassian.net/browse/%s\n")
      env = { "TASKS_FILE" => File.join(dir, "tasks.jsonl"),
              "TASKS_ARCHIVE" => File.join(dir, "archive.jsonl"),
              "XDG_CONFIG_HOME" => File.join(dir, "cfg"),
              "XDG_STATE_HOME" => File.join(dir, "state") }
      yield dir, env
    end
  end

  def cli(env, *args)
    out, err, st = Open3.capture3(env, "ruby", BIN, *args)
    [out.force_encoding("UTF-8"), err.force_encoding("UTF-8"), st]
  end

  def test_cli_links_lists_expanded_shorthands
    with_link_env do |_dir, env|
      out, _e, st = cli(env, "links", "--json")
      assert st.success?
      doc = JSON.parse(out)
      jira = doc["links"].find { |l| l["system"] == "jira" && l["task"].include?("One-link") }
      assert_equal "https://acme.atlassian.net/browse/OPS-7", jira["url"]
      assert_equal "jira:OPS-7", jira["label"]
    end
  end

  def test_cli_open_print_single_link
    with_link_env do |_dir, env|
      out, _e, st = cli(env, "open", "One-link", "--print")
      assert st.success?
      assert_equal "https://acme.atlassian.net/browse/OPS-7", out.strip
    end
  end

  def test_cli_open_multiple_links_lists_and_asks
    with_link_env do |_dir, env|
      out, err, st = cli(env, "open", "Many-link", "--print")
      refute st.success?
      assert_match(/2 links — pick one/, err)
      assert_match(/1\.\s+jira/, err)
      assert_empty out

      # pick composes with --system (indexes the filtered list)
      _o, err, = cli(env, "open", "Many-link", "--system", "jira", "--print", "0")
      assert_match(/no link #0/, err)

      # picking by number works
      out, _e, st = cli(env, "open", "Many-link", "2", "--print")
      assert st.success?
      assert_match(%r{slack\.com/archives/C1/p2}, out)

      # picking by system works
      out, _e, st = cli(env, "open", "Many-link", "--system", "jira", "--print")
      assert st.success?
      assert_match(/OPS-8/, out)

      # 0 and out-of-range picks abort instead of wrapping to the tail
      _o, err, st = cli(env, "open", "Many-link", "0", "--print")
      refute st.success?
      assert_match(/no link #0/, err)
      _o, err, st = cli(env, "open", "Many-link", "9", "--print")
      refute st.success?
      assert_match(/no link #9/, err)
    end
  end

  def test_cli_open_no_links_fails_cleanly
    with_link_env do |_dir, env|
      _o, err, st = cli(env, "open", "Linkless", "--print")
      refute st.success?
      assert_match(/no links on/, err)
    end
  end

  def test_cli_open_launches_via_tasks_opener
    with_link_env do |dir, env|
      # A fake opener that records its argv — proves the launch path without a browser.
      log = File.join(dir, "opened.txt")
      opener = File.join(dir, "fake-open")
      File.write(opener, "#!/bin/sh\necho \"$@\" >> #{log}\n")
      File.chmod(0o755, opener)

      out, _e, st = cli(env.merge("TASKS_OPENER" => opener), "open", "One-link")
      assert st.success?
      assert_match(/opened https/, out)
      # detached spawn — poll briefly for the fake opener's write
      50.times { break if File.exist?(log); sleep 0.05 }
      assert_match(%r{browse/OPS-7}, File.read(log))
    end
  end

  # -- TUI ---------------------------------------------------------------------

  def tui_app(content)
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), content)
      paths = Tasks::Config.for_dir(dir)
      paths.links = { "jira" => "https://acme.atlassian.net/browse/%s" }
      app = Tui::App.new(root: dir, paths: paths, llm_config: default_llm_config)
      app.instance_variable_get(:@ui).view = :next
      app.send(:rows)
      yield app
    end
  end

  def select_row(app, title)
    rws = app.instance_variable_get(:@rows)
    idx = rws.index { |r| r.item&.title&.include?(title) }
    raise "no row for #{title}" unless idx
    app.instance_variable_set(:@sel, idx)
  end

  def test_tui_open_link_flashes_and_launches
    tui_app(ORG_WITH_LINKS) do |app|
      select_row(app, "Many-link")
      opened = []
      Tasks::Opener.stub(:open_url, ->(url, **) { opened << url; true }) do
        app.send(:open_link)
      end
      assert_equal ["https://acme.atlassian.net/browse/OPS-8"], opened, "first link opens"
      assert_match(/opened jira.*1 of 2/, app.instance_variable_get(:@flash))
    end
  end

  def test_tui_open_link_without_links_flashes
    tui_app(ORG_WITH_LINKS) do |app|
      select_row(app, "Linkless")
      called = false
      Tasks::Opener.stub(:open_url, ->(*) { called = true }) do
        app.send(:open_link)
      end
      refute called
      assert_match(/no links/, app.instance_variable_get(:@flash))
    end
  end

  def test_tui_detail_panel_shows_links
    tui_app(ORG_WITH_LINKS) do |app|
      select_row(app, "One-link")
      app.send(:show_detail)
      text = app.instance_variable_get(:@ui).panel.lines.join("\n")
      plain = text.gsub(/\e\[[0-9;]*m/, "")
      assert_match(/links \(o opens the first\)/, plain)
      assert_match(%r{browse/OPS-7}, plain)
    end
  end
end
