# frozen_string_literal: true

require_relative "test_helper"
require "set"

class TestViews < Minitest::Test
  V = Tui::Views
  A = Tui::Ansi
  TODAY = Date.new(2026, 7, 1)

  def rows(view)
    with_store { |store, _o, _a| return V.rows(view, store.items, today: TODAY) }
  end

  def texts(rs) = rs.map { |r| A.strip(r.text) }

  # Count of leading spaces — the outliner indent depth of a stripped row.
  def indent(s) = s[/\A */].length

  # Build a store from records and yield it (mirrors the Dir.mktmpdir pattern
  # the derived-quadrant tests use). Returns whatever the block returns.
  def with_records(records)
    Dir.mktmpdir do |dir|
      path = File.join(dir, "tasks.jsonl")
      File.write(path, dump_fixture(records))
      return yield Tui::Store.new(org: path, archive: File.join(dir, "archive.jsonl"))
    end
  end

  # Tree-mode rows for `view`, as stripped-text strings.
  def tree_texts(store, view, collapsed: Set.new, show_deferred: false)
    V.rows(view, store.items, tree: store.tree, collapsed: collapsed,
                              show_deferred: show_deferred, today: TODAY, urgent_days: 3)
      .map { |r| A.strip(r.text) }
  end

  # A nested fixture: Work → "Ship release" (NEXT/A, due 07-03) with subtasks
  # "write notes" (TODO, due 07-05) → "grandchild next" (NEXT, undated),
  # "undated rider" (TODO), and a DONE "old subtask"; Home → "plan trip"
  # (INBOX) → "book hotel" (TODO, scheduled 07-02).
  NESTED = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "s1", "title" => "Work" },
    { "type" => "task", "id" => "p1", "parent" => "s1", "state" => "NEXT", "priority" => "A",
      "title" => "Ship release", "tags" => %w[@computer], "deadline" => "2026-07-03" },
    { "type" => "task", "id" => "c1", "parent" => "p1", "state" => "TODO",
      "title" => "write notes", "deadline" => "2026-07-05" },
    { "type" => "task", "id" => "g1", "parent" => "c1", "state" => "NEXT", "title" => "grandchild next" },
    { "type" => "task", "id" => "c2", "parent" => "p1", "state" => "TODO", "title" => "undated rider" },
    { "type" => "task", "id" => "c3", "parent" => "p1", "state" => "DONE", "title" => "old subtask",
      "closed" => "2026-06-01" },
    { "type" => "section", "id" => "s2", "title" => "Home" },
    { "type" => "task", "id" => "p2", "parent" => "s2", "state" => "INBOX", "title" => "plan trip" },
    { "type" => "task", "id" => "c4", "parent" => "p2", "state" => "TODO",
      "title" => "book hotel", "scheduled" => "2026-07-02" },
  ].freeze

  # -- tree rendering ------------------------------------------------------

  def test_children_render_indented_in_agenda
    with_records(NESTED) do |store|
      t = tree_texts(store, :agenda)
      parent = t.index { |s| s.include?("Ship release") }
      child  = t.index { |s| s.include?("write notes") }
      refute_nil parent
      assert_equal parent + 1, child, "child follows parent"
      assert_operator indent(t[child]), :>, indent(t[parent]), "child indented deeper"
    end
  end

  def test_children_render_indented_in_next
    with_records(NESTED) do |store|
      t = tree_texts(store, :next)
      parent = t.index { |s| s.include?("Ship release") }
      child  = t.index { |s| s.include?("write notes") }
      refute_nil parent
      assert_operator child, :>, parent
      assert_operator indent(t[child]), :>, indent(t[parent]), "child indented deeper"
    end
  end

  def test_children_render_indented_in_quadrants
    with_records(NESTED) do |store|
      t = tree_texts(store, :quadrants)
      parent = t.index { |s| s.include?("Ship release") }
      child  = t.index { |s| s.include?("write notes") }
      refute_nil parent
      assert_operator child, :>, parent
      assert_operator indent(t[child]), :>, indent(t[parent]), "child indented deeper"
    end
  end

  def test_children_render_indented_in_inbox
    with_records(NESTED) do |store|
      t = tree_texts(store, :inbox)
      parent = t.index { |s| s.include?("plan trip") }
      child  = t.index { |s| s.include?("book hotel") }
      refute_nil parent
      assert_equal parent + 1, child
      assert_operator indent(t[child]), :>, indent(t[parent]), "child indented deeper"
    end
  end

  # A dated parent keeps its agenda slot; its later-dated child rides beneath it
  # rather than sorting to its own (later) place.
  def test_subtree_rides_anchor_slot
    with_records(NESTED) do |store|
      t = tree_texts(store, :agenda)
      ship = t.index { |s| s.include?("Ship release") }
      notes = t.index { |s| s.include?("write notes") }
      # write notes is due 07-05 — later than plan-trip's 07-02 child, yet it
      # renders directly under Ship release (07-03), not sorted to the bottom.
      assert_equal ship + 1, notes
      assert_includes t[notes], "07-05"
    end
  end

  # Undated parent, dated child: the parent anchors at the child's date and both
  # render, parent first with a blanked stamp column.
  def test_agenda_fallback_anchor
    with_records(NESTED) do |store|
      t = tree_texts(store, :agenda)
      trip  = t.index { |s| s.include?("plan trip") }
      hotel = t.index { |s| s.include?("book hotel") }
      ship  = t.index { |s| s.include?("Ship release") }
      refute_nil trip
      assert_equal trip + 1, hotel, "parent first, child beneath"
      # anchored at the child's 07-02 (earlier than Ship release's 07-03)
      assert_operator trip, :<, ship
      # parent's own stamp column is blank (no DUE/STRT on the parent row)
      refute_includes t[trip], "STRT"
      refute_includes t[trip], "DUE"
      assert_includes t[hotel], "STRT"
    end
  end

  # ▾ marks an expanded parent, a 2-space pad marks a leaf.
  def test_markers_expanded_and_leaf
    with_records(NESTED) do |store|
      rs = V.rows(:next, store.items, tree: store.tree, today: TODAY, urgent_days: 3)
      ship  = rs.find { |r| r.item&.title == "Ship release" }
      rider = rs.find { |r| r.item&.title == "undated rider" }
      assert_includes A.strip(ship.text), "▾", "expanded parent shows ▾"
      leaf = A.strip(rider.text)
      refute_includes leaf, "▾"
      refute_includes leaf, "▸"
      # the leaf's marker column is two spaces where a parent's ▾/▸ would sit
      assert_includes leaf, "    undated rider" # base(2) + marker pad(2)
    end
  end

  # A collapsed id folds the subtree away: descendants vanish, the node shows ▸
  # and a dim (N) count of the hidden visible descendants.
  def test_collapsed_hides_descendants_with_count
    with_records(NESTED) do |store|
      t = tree_texts(store, :agenda, collapsed: Set["p1"])
      ship = t.find { |s| s.include?("Ship release") }
      assert_includes ship, "▸"
      # 3 visible descendants: write notes, grandchild next, undated rider
      # (the DONE "old subtask" is pruned, so it isn't counted).
      assert_includes ship, "(3)"
      refute t.any? { |s| s.include?("write notes") }, "collapsed subtree is hidden"
      refute t.any? { |s| s.include?("grandchild next") }
    end
  end

  # A deferred parent hides its whole subtree (defer the project → defer its
  # subtasks); Z (show_deferred) reveals both, parent carrying the ⏸ badge.
  def test_deferred_parent_hides_subtree
    recs = NESTED.map(&:dup)
    recs.find { |r| r["id"] == "p1" }["tags"] = %w[@computer defer]
    with_records(recs) do |store|
      hidden = tree_texts(store, :agenda)
      refute hidden.any? { |s| s.include?("Ship release") }, "deferred parent hidden"
      refute hidden.any? { |s| s.include?("write notes") }, "its subtree hidden too"

      shown = tree_texts(store, :agenda, show_deferred: true)
      ship = shown.find { |s| s.include?("Ship release") }
      refute_nil ship
      assert_includes ship, "⏸", "deferred styling preserved"
      assert shown.any? { |s| s.include?("write notes") }, "subtree revealed"
    end
  end

  # A DONE child under an open parent is pruned, like the flat views drop DONE.
  def test_non_open_child_not_rendered
    with_records(NESTED) do |store|
      %i[agenda next quadrants inbox].each do |view|
        refute tree_texts(store, view).any? { |s| s.include?("old subtask") },
               "DONE child leaked into #{view}"
      end
    end
  end

  # NEXT nested under NEXT renders once, under its parent anchor — never as its
  # own top-level Next entry (the maximal-NEXT rule).
  def test_maximal_next_renders_once
    with_records(NESTED) do |store|
      rs = V.rows(:next, store.items, tree: store.tree, today: TODAY, urgent_days: 3)
      gc = rs.select { |r| r.item&.title == "grandchild next" }
      assert_equal 1, gc.size, "grandchild NEXT renders exactly once"
      # it rides beneath its parent chain (indented), not as a group anchor
      assert_operator A.strip(gc.first.text).index("grandchild"), :>, 4
      # only the anchor's @computer context group exists (no bare grandchild group)
      headers = rs.reject(&:item).map { |r| A.strip(r.text) }.reject(&:empty?)
      assert_equal ["@computer"], headers
    end
  end

  # Task rows in tree mode carry their tree node; header/blank rows don't.
  def test_tree_rows_carry_nodes
    with_records(NESTED) do |store|
      rs = V.rows(:quadrants, store.items, tree: store.tree, today: TODAY, urgent_days: 3)
      rs.each do |r|
        if r.item then refute_nil r.node, "task row carries its node"
        else assert_nil r.node, "header/blank row has no node"
        end
      end
    end
  end

  # tree: nil must reproduce the pre-outliner flat output exactly — no markers,
  # no node member, identical to the direct flat-builder call.
  def test_flat_path_regression
    with_records(NESTED) do |store|
      items = store.items.reject(&:deferred?)
      %i[agenda next quadrants inbox].each do |view|
        flat = V.rows(view, items, today: TODAY, urgent_days: 3)
        flat.each do |r|
          assert_nil r.node, "flat row carries no node in #{view}"
          refute_includes r.text, "▾", "flat #{view} has no expand marker"
          refute_includes r.text, "▸", "flat #{view} has no collapse marker"
        end
      end
      # spot-check the exact legacy format for a dated agenda row
      agenda = V.rows(:agenda, items, today: TODAY)
      ship = A.strip(agenda.find { |r| r.item&.title == "Ship release" }.text)
      assert_equal "07-03 DUE  (in 2d)  [A] Ship release  @computer", ship
    end
  end

  def test_agenda_same_date_sorts_by_priority
    jsonl = dump_fixture([
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "cccc0001", "title" => "Work" },
      { "type" => "task", "id" => "cccc0002", "parent" => "cccc0001", "state" => "NEXT",
        "priority" => "B", "title" => "beta task tomorrow", "deadline" => "2026-07-02" },
      { "type" => "task", "id" => "cccc0003", "parent" => "cccc0001", "state" => "NEXT",
        "priority" => "A", "title" => "alpha task tomorrow", "deadline" => "2026-07-02" },
      { "type" => "task", "id" => "cccc0004", "parent" => "cccc0001", "state" => "NEXT",
        "title" => "no-priority task tomorrow", "deadline" => "2026-07-02" },
      { "type" => "task", "id" => "cccc0005", "parent" => "cccc0001", "state" => "NEXT",
        "priority" => "C", "title" => "later but urgent-ish", "deadline" => "2026-07-05" },
    ])
    Dir.mktmpdir do |dir|
      path = File.join(dir, "tasks.jsonl")
      File.write(path, jsonl)
      store = Tui::Store.new(org: path, archive: File.join(dir, "archive.jsonl"))
      titles = V.agenda(store.items, today: TODAY).map { |r| A.strip(r.text) }
      assert_operator titles.index { |t| t.include?("alpha") }, :<, titles.index { |t| t.include?("beta") }
      assert_operator titles.index { |t| t.include?("beta") }, :<, titles.index { |t| t.include?("no-priority") }
      # date still dominates: C-priority on a later date sorts below all of them
      assert_equal 3, titles.index { |t| t.include?("later but") }
    end
  end

  def test_agenda_sorted_soonest_first_and_selectable
    rs = rows(:agenda)
    assert_equal 2, rs.size # flight (deadline) + self-eval (scheduled)
    assert_includes rs[0].text, "Book flight"
    assert_includes rs[0].text, "DUE"
    assert_includes rs[1].text, "self-eval"
    assert_includes rs[1].text, "STRT"
    assert rs.all?(&:item), "agenda rows are all selectable"
  end

  def test_next_groups_by_context_with_unselectable_headers
    rs = rows(:next)
    headers = rs.reject(&:item).map { |r| A.strip(r.text) }.reject(&:empty?)
    assert_equal ["@computer", "@home"], headers
    flight_row = rs.find { |r| r.text.include?("Book flight") }
    assert flight_row.item
    assert_includes A.strip(flight_row.text), "7/2"
  end

  # Hybrid model keeps tagged fixture items where they were: the :important:/
  # :urgent: tags force their axes, and the fixture's A/B priorities line up.
  def test_quadrants_places_fixture_items
    rs = texts(rows(:quadrants))
    q1 = rs.index { |t| t.start_with?("Q1") }
    q2 = rs.index { |t| t.start_with?("Q2") }
    q3 = rs.index { |t| t.start_with?("Q3") }
    q4 = rs.index { |t| t.start_with?("Q4") }
    assert rs[q1...q2].any? { |t| t.include?("Book flight") }        # important+urgent
    assert rs[q2...q3].any? { |t| t.include?("Review PR backlog") }  # important only
    assert rs[q3...q4].any? { |t| t.include?("Travel desk") }        # urgent only
    assert rs[q4..].any? { |t| t.include?("Water the plants") }      # neither
  end

  # The point of the hybrid model: priority + deadline place a task with no
  # important/urgent tags at all.
  def test_quadrants_derived_from_priority_and_deadline
    jsonl = dump_fixture([
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "cccc0001", "title" => "Work" },
      { "type" => "task", "id" => "cccc0002", "parent" => "cccc0001", "state" => "NEXT",
        "priority" => "A", "title" => "alpha no date" },
      { "type" => "task", "id" => "cccc0003", "parent" => "cccc0001", "state" => "NEXT",
        "priority" => "B", "title" => "beta near deadline", "deadline" => "2026-07-02" },
      { "type" => "task", "id" => "cccc0004", "parent" => "cccc0001", "state" => "NEXT",
        "priority" => "C", "title" => "gamma near deadline", "deadline" => "2026-07-02" },
      { "type" => "task", "id" => "cccc0005", "parent" => "cccc0001", "state" => "NEXT",
        "title" => "delta far deadline", "deadline" => "2026-07-20" },
      { "type" => "task", "id" => "cccc0006", "parent" => "cccc0001", "state" => "TODO",
        "priority" => "A", "title" => "epsilon scheduled only", "scheduled" => "2026-07-02" },
    ])
    Dir.mktmpdir do |dir|
      path = File.join(dir, "tasks.jsonl")
      File.write(path, jsonl)
      store = Tui::Store.new(org: path, archive: File.join(dir, "archive.jsonl"))
      rs = texts(V.rows(:quadrants, store.items, today: TODAY, urgent_days: 3))
      q1 = rs.index { |t| t.start_with?("Q1") }
      q2 = rs.index { |t| t.start_with?("Q2") }
      q3 = rs.index { |t| t.start_with?("Q3") }
      q4 = rs.index { |t| t.start_with?("Q4") }
      assert rs[q1...q2].any? { |t| t.include?("beta") },    "B + near deadline → Q1"
      assert rs[q2...q3].any? { |t| t.include?("alpha") },   "A, no date → Q2"
      assert rs[q2...q3].any? { |t| t.include?("epsilon") }, "scheduled-only is not urgent → Q2"
      assert rs[q3...q4].any? { |t| t.include?("gamma") },   "C + near deadline → Q3"
      assert rs[q4..].any?   { |t| t.include?("delta") },    "far deadline → Q4"
    end
  end

  # A wider urgent_days window pulls a far-out deadline into the urgent column.
  def test_quadrants_urgent_days_widens_window
    jsonl = dump_fixture([
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "cccc0001", "title" => "Work" },
      { "type" => "task", "id" => "cccc0002", "parent" => "cccc0001", "state" => "NEXT",
        "priority" => "A", "title" => "far deadline task", "deadline" => "2026-07-20" },
    ])
    Dir.mktmpdir do |dir|
      path = File.join(dir, "tasks.jsonl")
      File.write(path, jsonl)
      store = Tui::Store.new(org: path, archive: File.join(dir, "archive.jsonl"))
      default = texts(V.rows(:quadrants, store.items, today: TODAY))
      q2 = default.index { |t| t.start_with?("Q2") }
      q3 = default.index { |t| t.start_with?("Q3") }
      assert default[q2...q3].any? { |t| t.include?("far deadline") }, "default 3d → Q2"

      wide = texts(V.rows(:quadrants, store.items, today: TODAY, urgent_days: 30))
      w1 = wide.index { |t| t.start_with?("Q1") }
      w2 = wide.index { |t| t.start_with?("Q2") }
      assert wide[w1...w2].any? { |t| t.include?("far deadline") }, "30d window → Q1"
    end
  end

  def test_inbox_lists_inbox_items
    rs = rows(:inbox)
    assert_equal 1, rs.size
    assert_includes rs[0].text, "garden"
    assert rs[0].item
  end

  def test_inbox_empty_state
    items = []
    rs = V.rows(:inbox, items, today: TODAY)
    assert_equal 1, rs.size
    assert_nil rs[0].item
    assert_includes A.strip(rs[0].text), "Inbox empty"
  end

  def test_done_items_never_appear_in_open_views
    %i[agenda next quadrants inbox].each do |view|
      refute texts(rows(view)).any? { |t| t.include?("Old finished thing") },
             "DONE item leaked into #{view}"
    end
  end

  # An open task under a CLOSED (DONE) parent is not dropped: it is hoisted to
  # anchor level and renders in every view its state/date qualifies it for,
  # exactly once, while the DONE parent itself stays pruned. A closed node under
  # a hidden DEFERRED parent, by contrast, stays hidden — defer-hiding wins.
  HOIST = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "s1", "title" => "Work" },
    { "type" => "task", "id" => "done1", "parent" => "s1", "state" => "DONE",
      "title" => "done project", "closed" => "2026-06-01" },
    { "type" => "task", "id" => "oc", "parent" => "done1", "state" => "TODO",
      "title" => "open child hoisted", "deadline" => "2026-07-04" },
    { "type" => "task", "id" => "nc", "parent" => "done1", "state" => "NEXT",
      "title" => "next child hoisted", "tags" => %w[@work] },
    { "type" => "task", "id" => "ic", "parent" => "done1", "state" => "INBOX",
      "title" => "inbox child hoisted" },
    { "type" => "section", "id" => "s2", "title" => "Someday" },
    { "type" => "task", "id" => "def1", "parent" => "s2", "state" => "TODO",
      "title" => "someday project", "tags" => %w[defer] },
    { "type" => "task", "id" => "ddone", "parent" => "def1", "state" => "DONE",
      "title" => "buried done", "closed" => "2026-06-01" },
    { "type" => "task", "id" => "hg", "parent" => "ddone", "state" => "TODO",
      "title" => "hidden grandchild", "deadline" => "2026-07-04" },
  ].freeze

  def count(rows_texts, title) = rows_texts.count { |s| s.include?(title) }

  def test_open_child_under_done_parent_is_hoisted_into_every_view
    with_records(HOIST) do |store|
      ag = tree_texts(store, :agenda)
      # the hoisted dated child renders once, at anchor level (depth 0 = the
      # bare marker column, two leading spaces — not indented under a parent).
      assert_equal 1, count(ag, "open child hoisted")
      oc = ag.find { |s| s.include?("open child hoisted") }
      assert_equal 2, indent(oc), "hoisted anchor renders at depth 0"
      assert_includes oc, "07-04", "keeps its own date"
      refute ag.any? { |s| s.include?("done project") }, "DONE parent does not render"

      quad = tree_texts(store, :quadrants)
      assert_equal 1, count(quad, "open child hoisted")
      assert_equal 1, count(quad, "next child hoisted")
      assert_equal 1, count(quad, "inbox child hoisted")
      refute quad.any? { |s| s.include?("done project") }

      nxt = tree_texts(store, :next)
      assert_equal 1, count(nxt, "next child hoisted"), "hoisted NEXT anchors in Next"

      inb = tree_texts(store, :inbox)
      assert_equal 1, count(inb, "inbox child hoisted"), "hoisted INBOX anchors in Inbox"

      # defer-hiding wins over hoisting: the open grandchild under a DONE node
      # under a hidden DEFERRED parent stays hidden in every view.
      %i[agenda next quadrants inbox].each do |view|
        t = tree_texts(store, view)
        refute t.any? { |s| s.include?("hidden grandchild") },
               "deferred subtree stays hidden in #{view} (hoist doesn't defeat defer)"
      end
    end
  end

  # A broad adversarial fixture: nested NEXT/TODO/INBOX, DONE middles with open
  # grandchildren (hoisted), a deferred branch (hidden), single-context anchors,
  # dated and undated. Each view renders its qualifying open tasks exactly once.
  BROAD = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "s1", "title" => "Work" },
    { "type" => "task", "id" => "n1", "parent" => "s1", "state" => "NEXT",
      "title" => "alpha next", "tags" => %w[@work], "deadline" => "2026-07-03" },
    { "type" => "task", "id" => "t1", "parent" => "n1", "state" => "TODO", "title" => "alpha sub todo" },
    { "type" => "task", "id" => "n2", "parent" => "t1", "state" => "NEXT", "title" => "alpha sub next" },
    { "type" => "task", "id" => "d1", "parent" => "n1", "state" => "DONE",
      "title" => "alpha done mid", "closed" => "2026-06-01" },
    { "type" => "task", "id" => "t2", "parent" => "d1", "state" => "TODO",
      "title" => "alpha hoisted todo", "deadline" => "2026-07-06" },
    { "type" => "task", "id" => "n3", "parent" => "d1", "state" => "NEXT", "title" => "alpha hoisted next" },
    { "type" => "task", "id" => "t3", "parent" => "s1", "state" => "TODO", "title" => "beta todo", "tags" => %w[@work] },
    { "type" => "section", "id" => "s2", "title" => "Personal" },
    { "type" => "task", "id" => "i1", "parent" => "s2", "state" => "INBOX", "title" => "gamma inbox" },
    { "type" => "task", "id" => "t4", "parent" => "i1", "state" => "TODO",
      "title" => "gamma sub todo", "scheduled" => "2026-07-02" },
    { "type" => "task", "id" => "i2", "parent" => "i1", "state" => "INBOX", "title" => "gamma sub inbox" },
    { "type" => "section", "id" => "s3", "title" => "Someday" },
    { "type" => "task", "id" => "dd1", "parent" => "s3", "state" => "TODO",
      "title" => "delta deferred", "tags" => %w[defer] },
    { "type" => "task", "id" => "t5", "parent" => "dd1", "state" => "TODO", "title" => "delta buried" },
  ].freeze

  def test_every_open_task_renders_exactly_once_per_view
    with_records(BROAD) do |store|
      hidden = ["delta deferred", "delta buried", "alpha done mid"]

      # quadrants shows every open non-deferred task exactly once.
      quad = tree_texts(store, :quadrants)
      all_open = ["alpha next", "alpha sub todo", "alpha sub next", "alpha hoisted todo",
                  "alpha hoisted next", "beta todo", "gamma inbox", "gamma sub todo", "gamma sub inbox"]
      all_open.each { |t| assert_equal 1, count(quad, t), "#{t} once in quadrants" }
      hidden.each { |t| assert_equal 0, count(quad, t), "#{t} absent from quadrants" }

      # next: every open task under a maximal-NEXT anchor (n1's subtree) plus the
      # hoisted NEXT (n3), each once; nothing else.
      nxt = tree_texts(store, :next)
      ["alpha next", "alpha sub todo", "alpha sub next", "alpha hoisted next"].each do |t|
        assert_equal 1, count(nxt, t), "#{t} once in next"
      end
      ["alpha hoisted todo", "beta todo", "gamma inbox", "gamma sub todo", "gamma sub inbox", *hidden].each do |t|
        assert_equal 0, count(nxt, t), "#{t} not in next"
      end

      # inbox: the maximal-INBOX anchor and its subtree, each once.
      inb = tree_texts(store, :inbox)
      ["gamma inbox", "gamma sub todo", "gamma sub inbox"].each do |t|
        assert_equal 1, count(inb, t), "#{t} once in inbox"
      end
      ["alpha next", "beta todo", *hidden].each { |t| assert_equal 0, count(inb, t), "#{t} not in inbox" }

      # agenda: every open task belonging to a dated-containing subtree, once.
      ag = tree_texts(store, :agenda)
      ["alpha next", "alpha sub todo", "alpha sub next", "alpha hoisted todo",
       "gamma inbox", "gamma sub todo", "gamma sub inbox"].each do |t|
        assert_equal 1, count(ag, t), "#{t} once in agenda"
      end
      # undated leaves with no dated descendant don't reach the agenda.
      ["alpha hoisted next", "beta todo", *hidden].each { |t| assert_equal 0, count(ag, t), "#{t} not in agenda" }
    end
  end

  # Accepted behavior: a NEXT anchor tagged with two contexts renders its whole
  # subtree once under EACH context group (the subtree rides per group).
  def test_multi_context_next_anchor_duplicates_subtree_per_group
    recs = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "s1", "title" => "Work" },
      { "type" => "task", "id" => "m1", "parent" => "s1", "state" => "NEXT",
        "title" => "multi anchor", "tags" => %w[@alpha @beta] },
      { "type" => "task", "id" => "mc", "parent" => "m1", "state" => "TODO", "title" => "multi child" },
    ]
    with_records(recs) do |store|
      rs = V.rows(:next, store.items, tree: store.tree, today: TODAY, urgent_days: 3)
      headers = rs.reject(&:item).map { |r| A.strip(r.text) }.reject(&:empty?)
      assert_equal ["@alpha", "@beta"], headers
      t = rs.map { |r| A.strip(r.text) }
      assert_equal 2, count(t, "multi anchor"), "anchor renders once per context group"
      assert_equal 2, count(t, "multi child"), "its subtree rides each group"
    end
  end

  def test_recurring_task_gets_a_badge
    jsonl = dump_fixture([
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "cccc0001", "title" => "Work" },
      { "type" => "task", "id" => "cccc0002", "parent" => "cccc0001", "state" => "NEXT",
        "title" => "Pay rent", "tags" => %w[@home], "deadline" => "2026-07-02", "recur" => "+1m" },
      { "type" => "task", "id" => "cccc0003", "parent" => "cccc0001", "state" => "NEXT",
        "title" => "Plain task", "deadline" => "2026-07-02" },
    ])
    Dir.mktmpdir do |dir|
      path = File.join(dir, "tasks.jsonl")
      File.write(path, jsonl)
      store = Tui::Store.new(org: path, archive: File.join(dir, "archive.jsonl"))
      rs = V.agenda(store.items, today: TODAY)
      rent = rs.find { |r| r.text.include?("Pay rent") }
      plain = rs.find { |r| r.text.include?("Plain task") }
      assert_includes rent.text, "↻", "recurring task shows the ↻ badge"
      refute_includes plain.text, "↻", "non-recurring task has no badge"
    end
  end
end
