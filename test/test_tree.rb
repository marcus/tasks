# frozen_string_literal: true

require_relative "test_helper"
require "open3"
require "json"

# Coverage for the structural index (Tasks::Tree), link extraction
# (Tasks::Links), and the surfaces they back: Store#tree/node_for/body/links,
# `tasks links`, and `list --body` full-text search.
class TestTree < Minitest::Test
  NESTED_ORG = <<~ORG
    * Work
    ** NEXT [#A] Fix billing outage :@computer:
       DEADLINE: <2026-07-10 Fri>
       :PROPERTIES:
       :ID: aaaa1111
       :END:
       Context in [[https://acme.slack.com/archives/C042/p171][the incident thread]].
       Ticket: https://acme.atlassian.net/browse/OPS-1234.
    ** NEXT Review Q3 planning doc
       https://docs.google.com/document/d/abc/edit
    *** TODO Leave comments for Dana
       Dana prefers suggestions mode.
    * Home
    ** TODO Renew passport
       Photo specs: https://travel.state.gov/photos.html, then book.
  ORG

  def nested_store
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, NESTED_ORG)
      yield Tasks::Store.new(org: org, archive: File.join(dir, "archive.org")), org, dir
    end
  end

  # -- tree structure ----------------------------------------------------------

  def test_tree_nests_sections_tasks_and_subtasks
    nested_store do |store, _o, _d|
      roots = store.tree
      assert_equal %w[Work Home], roots.map(&:title)
      work = roots[0]
      assert work.section?
      assert_equal 2, work.children.size
      review = work.children[1]
      assert review.task?
      assert_equal "Leave comments for Dana", review.children[0].item.title
      assert_equal review, review.children[0].parent
    end
  end

  def test_node_body_is_own_lines_only
    nested_store do |store, _o, _d|
      review = store.node_for(store.items.find { |i| i.title.include?("Review Q3") })
      assert_match(/docs.google/, review.body_text)
      refute_match(/suggestions mode/, review.body_text, "child's body is not the parent's")
    end
  end

  def test_node_project_is_nearest_ancestor
    nested_store do |store, _o, _d|
      dana = store.node_for(store.items.find { |i| i.title.include?("Dana") })
      assert_equal "Review Q3 planning doc", dana.project.title
      assert_equal "Work", dana.project.project.title
    end
  end

  def test_tree_rebuilds_after_external_change
    nested_store do |store, org, _d|
      assert_equal 2, store.tree.size
      future = Time.now + 2
      File.write(org, NESTED_ORG + "* Errands\n** TODO Buy stamps\n")
      File.utime(future, future, org)
      assert_equal 3, store.tree.size, "tree follows the file, like items"
    end
  end

  def test_body_strips_drawer_and_covers_archive_items
    nested_store do |store, _o, _d|
      billing = store.items.find { |i| i.title.include?("billing") }
      body = store.body(billing).join
      refute_match(/:PROPERTIES:|:ID:/, body, "drawer is machinery, not body")
      assert_match(/incident thread/, body)

      store.complete!(billing)
      store.archive_swept!
      archived = store.archive_items.find { |i| i.title.include?("billing") }
      assert_match(/incident thread/, store.body(archived).join, "body works for archive items")
      assert_nil store.node_for(archived), "tree indexes the live file only"
    end
  end

  # -- link extraction ---------------------------------------------------------

  def test_extract_org_links_with_labels
    links = Tasks::Links.extract("See [[https://acme.slack.com/archives/C1/p2][the thread]] today.")
    assert_equal 1, links.size
    assert_equal "https://acme.slack.com/archives/C1/p2", links[0].url
    assert_equal "the thread", links[0].label
    assert_equal "slack", links[0].system
  end

  def test_extract_bare_urls_trims_sentence_punctuation
    links = Tasks::Links.extract("Ticket: https://acme.atlassian.net/browse/OPS-9.")
    assert_equal "https://acme.atlassian.net/browse/OPS-9", links[0].url
    assert_equal "jira", links[0].system
  end

  def test_extract_dedupes_and_prefers_the_labeled_form
    text = "[[https://a.co/x][labeled]] and again https://a.co/x in prose"
    links = Tasks::Links.extract(text)
    assert_equal 1, links.size
    assert_equal "labeled", links[0].label
  end

  def test_classify_known_systems_and_fallback
    { "https://acme.slack.com/x"            => "slack",
      "https://acme.atlassian.net/browse/1" => "jira",
      "https://jira.acme.com/browse/1"      => "jira",
      "https://github.com/a/b/pull/1"       => "github",
      "https://linear.app/acme/issue/T-1"   => "linear",
      "https://docs.google.com/document/d/x" => "gdocs",
      "https://internal.acme.dev/runbook"   => "internal.acme.dev",
      "https://www.example.com/page"        => "example.com" }.each do |url, want|
      assert_equal want, Tasks::Links.classify(url), url
    end
  end

  def test_classify_survives_an_unparseable_url
    assert_equal "link", Tasks::Links.classify("https://[bad")
  end

  def test_extract_ignores_plain_prose
    assert_empty Tasks::Links.extract("nothing to see here, not even ftp://old.school")
  end

  def test_extract_ignores_org_internal_links
    text = "See [[My Heading]] and [[id:abc-123][the task]] and [[file:notes.org][notes]]."
    assert_empty Tasks::Links.extract(text), "org navigation is not a web link"
  end

  def test_extract_keeps_balanced_parens_in_urls
    links = Tasks::Links.extract("Read https://en.wikipedia.org/wiki/Ruby_(programming_language) today")
    assert_equal "https://en.wikipedia.org/wiki/Ruby_(programming_language)", links[0].url
  end

  def test_extract_returns_unbalanced_paren_to_the_sentence
    links = Tasks::Links.extract("(see https://a.co/x)")
    assert_equal "https://a.co/x", links[0].url
  end

  def test_extract_trims_unicode_punctuation_and_verbatim_markers
    assert_equal "https://a.co/x", Tasks::Links.extract("see https://a.co/x…")[0].url
    assert_equal "https://b.co/y", Tasks::Links.extract("=https://b.co/y= done")[0].url
    # a URL genuinely ending in '=' (query param) survives when not verbatim-wrapped
    assert_equal "https://c.co/?t=abc=", Tasks::Links.extract("go https://c.co/?t=abc= now")[0].url
  end

  def test_extract_drops_scheme_only_fragments
    links = Tasks::Links.extract("use https://, not http://, as the prefix")
    assert_empty links, "a bare scheme with no host is prose, not a link"
  end

  def test_classify_fallback_is_lowercased
    assert_equal "example.com", Tasks::Links.classify("https://Example.COM/x")
    assert_equal "example.com", Tasks::Links.classify("https://WWW.example.com/x")
  end

  def test_node_for_refuses_a_different_task_at_a_held_line
    nested_store do |store, org, _d|
      held = store.items.find { |i| i.title.include?("Renew passport") }
      held = held.dup
      held.id = nil # an id-less held item
      # An out-of-band edit shifts lines so a different task sits at held.line.
      future = Time.now + 2
      File.write(org, "* New\n** TODO interloper task\n" + File.read(org))
      File.utime(future, future, org)
      assert_empty store.body(held), "an id-less stale item degrades to empty, not to the wrong task"
    end
  end

  def test_classify_confluence_on_atlassian_by_path
    assert_equal "confluence", Tasks::Links.classify("https://acme.atlassian.net/wiki/spaces/ENG/pages/1")
    assert_equal "jira",       Tasks::Links.classify("https://acme.atlassian.net/browse/OPS-1")
  end

  def test_body_excludes_planning_and_archive_separators
    nested_store do |store, _o, _d|
      billing = store.items.find { |i| i.title.include?("billing") }
      refute_match(/DEADLINE/, store.body(billing).join, "planning stamps are not prose")

      store.complete!(billing)
      store.archive_swept!
      archived = store.archive_items.find { |i| i.title.include?("billing") }
      refute_match(/# Archived/, store.body(archived).join, "sweep separators are not prose")
    end
  end

  def test_store_links_includes_title_urls
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, "* Work\n** TODO Review https://github.com/acme/app/pull/7\n")
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.org"))
      links = store.links(store.items.first)
      assert_equal ["github"], links.map(&:system)
    end
  end

  # -- CLI end-to-end ----------------------------------------------------------

  BIN = File.expand_path("../bin/tasks", __dir__)

  def cli(dir, *args)
    env = { "TASKS_ORG" => File.join(dir, "gtd.org"),
            "TASKS_ARCHIVE" => File.join(dir, "archive.org"),
            "XDG_STATE_HOME" => File.join(dir, "state") }
    out, err, st = Open3.capture3(env, "ruby", BIN, *args)
    [out.force_encoding("UTF-8"), err.force_encoding("UTF-8"), st]
  end

  def with_cli_fixture
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "gtd.org"), NESTED_ORG)
      yield dir
    end
  end

  def test_cli_links_lists_and_filters_by_system
    with_cli_fixture do |dir|
      out, _e, st = cli(dir, "links")
      assert st.success?
      assert_match(/slack\s+https:\/\/acme\.slack\.com/, out)
      assert_match(/jira\s+https:\/\/acme\.atlassian\.net/, out)

      out, _e, st = cli(dir, "links", "--system", "jira")
      assert st.success?
      assert_match(/jira/, out)
      refute_match(/slack\.com/, out)

      # case-insensitive system filter
      out, _e, st = cli(dir, "links", "--system", "JIRA")
      assert st.success?
      assert_match(/atlassian/, out)
    end
  end

  def test_cli_links_rejects_bad_flags
    with_cli_fixture do |dir|
      _o, err, st = cli(dir, "links", "-j")
      refute st.success?
      assert_match(/unknown flag: -j/, err)

      _o, err, st = cli(dir, "links", "--system", "--json")
      refute st.success?
      assert_match(/--system needs a value/, err)
    end
  end

  def test_cli_links_json_and_single_ref
    with_cli_fixture do |dir|
      out, _e, st = cli(dir, "links", "billing outage", "--json")
      assert st.success?
      doc = JSON.parse(out)
      systems = doc["links"].map { |l| l["system"] }
      assert_includes systems, "slack"
      assert_includes systems, "jira"
      assert(doc["links"].all? { |l| l["task"].include?("billing") })
    end
  end

  def test_cli_links_empty_message
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "gtd.org"), "* Inbox\n** TODO nothing linked\n")
      out, _e, st = cli(dir, "links")
      assert st.success?
      assert_match(/No links found/, out)
    end
  end

  def test_cli_list_body_flag_searches_notes
    with_cli_fixture do |dir|
      out, _e, st = cli(dir, "list", "/suggestions", "-b")
      assert st.success?
      assert_match(/Leave comments for Dana/, out)

      out, _e, st = cli(dir, "list", "/suggestions")
      assert st.success?
      assert_match(/No matching tasks/, out, "without -b the body is not searched")
    end
  end

  def test_cli_show_reports_project_and_links
    with_cli_fixture do |dir|
      out, _e, st = cli(dir, "show", "billing outage")
      assert st.success?
      assert_match(/project:\s+Work/, out)
      assert_match(/slack\s+https/, out)

      out, _e, = cli(dir, "show", "billing outage", "--json")
      doc = JSON.parse(out)
      assert_equal "Work", doc["project"]
      assert_equal %w[slack jira], doc["links"].map { |l| l["system"] }
    end
  end
end
