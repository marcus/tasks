# frozen_string_literal: true

require_relative "test_helper"
require "set"
require "tasks/application"

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

  # Tree-mode Rows for `view` (keeps the Row so tests can reach r.node.level —
  # the true nesting depth, decoupled from whatever indent glyph the outliner
  # renders).
  def tree_rows(store, view, collapsed: Set.new, show_deferred: false)
    V.rows(view, store.items, tree: store.tree, collapsed: collapsed,
                              show_deferred: show_deferred, today: TODAY, urgent_days: 3)
  end

  # Tree-mode rows for `view`, as stripped-text strings.
  def tree_texts(store, view, collapsed: Set.new, show_deferred: false)
    tree_rows(store, view, collapsed: collapsed, show_deferred: show_deferred)
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

  # Nesting is asserted on the true tree depth (node.level), not by counting
  # leading spaces — the outliner now drops a │ thread-glyph (not a space) at the
  # head of a nested row, so screen-scraped indent no longer tracks depth. As a
  # visible-rendering check we additionally assert the child row carries the
  # thread glyph the parent (depth 0) lacks.
  def find_row(rows, needle) = rows.find { |r| A.strip(r.text).include?(needle) }

  def test_children_render_indented_in_agenda
    with_records(NESTED) do |store|
      rs = tree_rows(store, :agenda)
      pi = rs.index { |r| A.strip(r.text).include?("Ship release") }
      ci = rs.index { |r| A.strip(r.text).include?("write notes") }
      refute_nil pi
      assert_equal pi + 1, ci, "child follows parent"
      assert_operator rs[ci].node.level, :>, rs[pi].node.level, "child nests deeper"
      assert_includes rs[ci].text, "│", "nested row carries the thread glyph"
      refute_includes rs[pi].text, "│", "the depth-0 anchor has no thread glyph"
    end
  end

  def test_children_render_indented_in_next
    with_records(NESTED) do |store|
      rs = tree_rows(store, :next)
      pi = rs.index { |r| A.strip(r.text).include?("Ship release") }
      ci = rs.index { |r| A.strip(r.text).include?("write notes") }
      refute_nil pi
      assert_operator ci, :>, pi
      assert_operator rs[ci].node.level, :>, rs[pi].node.level, "child nests deeper"
      assert_includes rs[ci].text, "│", "nested row carries the thread glyph"
    end
  end

  def test_children_render_indented_in_quadrants
    with_records(NESTED) do |store|
      rs = tree_rows(store, :quadrants)
      pi = rs.index { |r| A.strip(r.text).include?("Ship release") }
      ci = rs.index { |r| A.strip(r.text).include?("write notes") }
      refute_nil pi
      assert_operator ci, :>, pi
      assert_operator rs[ci].node.level, :>, rs[pi].node.level, "child nests deeper"
      assert_includes rs[ci].text, "│", "nested row carries the thread glyph"
    end
  end

  def test_future_available_from_child_is_hidden_under_visible_inbox_parent
    with_records(NESTED) do |store|
      rs = tree_rows(store, :inbox)
      pi = rs.index { |r| A.strip(r.text).include?("plan trip") }
      ci = rs.index { |r| A.strip(r.text).include?("book hotel") }
      refute_nil pi
      assert_nil ci, "future child stays unavailable before its Available from date"

      revealed = tree_rows(store, :inbox, show_deferred: true)
      revealed_parent = revealed.index { |r| A.strip(r.text).include?("plan trip") }
      revealed_child = revealed.index { |r| A.strip(r.text).include?("book hotel") }
      assert_equal revealed_parent + 1, revealed_child
      assert_operator revealed[revealed_child].node.level, :>, revealed[revealed_parent].node.level
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
  def test_unavailable_dated_child_only_anchors_agenda_when_revealed
    with_records(NESTED) do |store|
      t = tree_texts(store, :agenda)
      trip  = t.index { |s| s.include?("plan trip") }
      hotel = t.index { |s| s.include?("book hotel") }
      ship  = t.index { |s| s.include?("Ship release") }
      assert_nil trip
      assert_nil hotel

      t = tree_texts(store, :agenda, show_deferred: true)
      trip  = t.index { |s| s.include?("plan trip") }
      hotel = t.index { |s| s.include?("book hotel") }
      ship  = t.index { |s| s.include?("Ship release") }
      refute_nil trip
      assert_equal trip + 1, hotel, "parent first, child beneath"
      # anchored at the child's 07-02 (earlier than Ship release's 07-03)
      assert_operator trip, :<, ship
      # parent's own stamp column is blank (no DUE/AVL on the parent row)
      refute_includes t[trip], "AVL"
      refute_includes t[trip], "DUE"
      assert_includes t[hotel], "AVL"
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
      # the leaf's marker column is two spaces where a parent's ▾/▸ would sit,
      # preceded by the depth-1 thread glyph
      assert_includes leaf, "│   undated rider" # thread(│ ) + marker pad(2)
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

  def test_available_from_boundary_and_reveal_badges_use_canonical_availability
    records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "s1", "title" => "Work" },
      { "type" => "task", "id" => "past", "parent" => "s1", "state" => "NEXT",
        "title" => "past release", "scheduled" => "2026-06-30" },
      { "type" => "task", "id" => "today", "parent" => "s1", "state" => "NEXT",
        "title" => "today release", "scheduled" => "2026-07-01" },
      { "type" => "task", "id" => "future", "parent" => "s1", "state" => "NEXT",
        "title" => "future release", "scheduled" => "2026-07-05" },
      { "type" => "task", "id" => "held", "parent" => "s1", "state" => "NEXT",
        "title" => "indefinite hold", "tags" => %w[defer] },
    ]
    with_records(records) do |store|
      reader = Tasks::TaskReadModel.new(store.read_snapshot, today: TODAY)
      visible = texts(V.rows(:next, store.items, tree: store.tree, today: TODAY,
                                    show_deferred: false, reader: reader))
      assert visible.any? { |text| text.include?("past release") }
      assert visible.any? { |text| text.include?("today release") }
      refute visible.any? { |text| text.include?("future release") }
      refute visible.any? { |text| text.include?("indefinite hold") }

      revealed = texts(V.rows(:next, store.items, tree: store.tree, today: TODAY,
                                     show_deferred: true, reader: reader))
      assert_includes revealed.find { |text| text.include?("future release") }, "⏳ 7/5"
      assert_includes revealed.find { |text| text.include?("indefinite hold") }, "⏸"
      refute_includes revealed.find { |text| text.include?("today release") }, "⏳"
    end
  end

  def test_ancestor_availability_hides_descendants_through_closed_hoisting
    records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "s1", "title" => "Work" },
      { "type" => "task", "id" => "parent", "parent" => "s1", "state" => "DONE",
        "title" => "closed timed parent", "scheduled" => "2026-07-09", "closed" => "2026-06-01" },
      { "type" => "task", "id" => "child", "parent" => "parent", "state" => "NEXT",
        "title" => "hoisted blocked child", "deadline" => "2026-07-02" },
    ]
    with_records(records) do |store|
      reader = Tasks::TaskReadModel.new(store.read_snapshot, today: TODAY)
      hidden = texts(V.rows(:next, store.items, tree: store.tree, today: TODAY,
                                   show_deferred: false, reader: reader))
      refute hidden.any? { |text| text.include?("hoisted blocked child") }

      revealed_rows = V.rows(:next, store.items, tree: store.tree, today: TODAY,
                                    show_deferred: true, reader: reader)
      child = revealed_rows.find { |row| row.item&.id == "child" }
      refute_nil child
      refute_includes A.strip(child.text), "│", "closed ancestor is skipped and child is hoisted"
      assert_includes A.strip(child.text), "⏳ 7/9 ↑"
      refute texts(revealed_rows).any? { |text| text.include?("closed timed parent") }
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

  def test_outline_renders_every_live_section_and_task_in_canonical_dfs_order
    with_records(NESTED) do |store|
      rs = tree_rows(store, :outline)
      assert_equal [
        nil, "Ship release", "write notes", "grandchild next", "undated rider",
        "old subtask", nil, "plan trip", "book hotel",
      ], rs.map { |row| row.item&.title }
      assert_equal ["Work", "Home"], rs.reject(&:item).map { |row| A.strip(row.text).strip }
      assert_includes A.strip(find_row(rs, "old subtask").text), "DONE"
      assert_includes A.strip(find_row(rs, "book hotel").text), "TODO"
      assert rs.reject(&:item).all? { |row| row.node.nil? }, "sections remain non-selectable structure rows"
    end
  end

  def test_outline_collapse_hides_closed_and_unavailable_descendants_without_reordering_siblings
    with_records(NESTED) do |store|
      rs = tree_rows(store, :outline, collapsed: Set["p1"])
      assert_equal ["Ship release", "plan trip", "book hotel"], rs.filter_map { |row| row.item&.title }
      ship = find_row(rs, "Ship release")
      assert_includes A.strip(ship.text), "(4)"
    end
  end

  def test_filtered_outline_is_flat_and_contains_only_supplied_matches
    with_records(NESTED) do |store|
      matches = store.items.select { |item| item.title.include?("task") || item.title.include?("notes") }
      rs = V.rows(:outline, matches, today: TODAY, store: store)
      assert_equal matches.map(&:id), rs.map { |row| row.item.id }
      assert rs.all? { |row| row.node.nil? }
    end
  end

  def test_outline_keeps_nested_sections_as_non_selectable_structure_rows
    records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "aa000001", "title" => "Projects" },
      { "type" => "section", "id" => "aa000002", "parent" => "aa000001", "title" => "Launch" },
      { "type" => "task", "id" => "aa000003", "parent" => "aa000002", "state" => "DONE",
        "title" => "Shipped", "closed" => "2026-07-10" },
    ]
    with_records(records) do |store|
      rs = tree_rows(store, :outline)
      assert_equal [nil, nil, "Shipped"], rs.map { |row| row.item&.title }
      assert_equal ["Projects", "Launch"], rs.reject(&:item).map { |row| A.strip(row.text).strip }
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

  # The Query is the semantic source of truth for both render modes. Flat mode
  # renders exactly the matching set; tree mode must contain every match but may
  # additionally show open descendants as contextual riders.
  def test_flat_and_tree_modes_share_canonical_eligibility
    with_records(NESTED) do |store|
      %i[agenda next quadrants inbox projects].each do |view|
        query = V.view_query(view, today: TODAY, urgent_days: 3, show_deferred: false, store: store)
        eligible = query.select(store.items).map(&:id).to_set
        flat = V.rows(view, store.items, today: TODAY, urgent_days: 3, store: store)
                .filter_map { |row| row.item&.id }.to_set
        tree = V.rows(view, store.items, tree: store.tree, today: TODAY, urgent_days: 3,
                                        store: store)
                .filter_map { |row| row.item&.id }.to_set

        assert_equal eligible, flat, "flat #{view} renders exactly the query matches"
        assert eligible.subset?(tree), "tree #{view} retains every query match"
      end
    end
  end

  def test_availability_matrix_keeps_flat_tree_reveal_and_project_counts_aligned
    records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "fa000001", "title" => "Work" },
      { "type" => "task", "id" => "fa000002", "parent" => "fa000001", "state" => "NEXT",
        "title" => "Timed next", "scheduled" => "2026-07-05" },
      { "type" => "task", "id" => "fa000003", "parent" => "fa000002", "state" => "WAITING",
        "title" => "Timed next rider" },
      { "type" => "task", "id" => "fa000004", "parent" => "fa000001", "state" => "INBOX",
        "title" => "Held inbox", "tags" => %w[defer] },
      { "type" => "task", "id" => "fa000005", "parent" => "fa000004", "state" => "TODO",
        "title" => "Held inbox rider" },
      { "type" => "task", "id" => "fa000006", "parent" => "fa000001", "state" => "DONE",
        "title" => "Closed transparent", "closed" => "2026-06-01" },
      { "type" => "task", "id" => "fa000007", "parent" => "fa000006", "state" => "NEXT",
        "title" => "Hoisted next" },
      { "type" => "task", "id" => "fa000008", "parent" => "fa000001", "state" => "TODO",
        "title" => "Dated available", "deadline" => "2026-07-02" },
      { "type" => "task", "id" => "fa000009", "parent" => "fa000008", "state" => "TODO",
        "title" => "Agenda rider" },
    ]

    with_records(records) do |store|
      %i[agenda next quadrants inbox projects].each do |view|
        [false, true].each do |revealed|
          query = V.view_query(
            view, today: TODAY, urgent_days: 3, show_deferred: revealed, store: store
          )
          eligible = query.select(store.items).map(&:id).to_set
          flat = V.rows(
            view, store.items, today: TODAY, urgent_days: 3,
            show_deferred: revealed, store: store
          ).filter_map { |row| row.item&.id }
          tree = V.rows(
            view, store.items, tree: store.tree, today: TODAY, urgent_days: 3,
            show_deferred: revealed, store: store
          ).filter_map { |row| row.item&.id }

          assert_equal eligible, flat.to_set,
                       "flat/filter #{view} reveal=#{revealed} uses canonical eligibility"
          assert eligible.subset?(tree.to_set),
                 "tree #{view} reveal=#{revealed} contains every canonical anchor"
          assert_equal tree.uniq, tree, "tree #{view} reveal=#{revealed} renders no duplicate anchors/riders"
        end
      end

      hidden_next = tree_rows(store, :next).filter_map { |row| row.item&.id }
      refute_includes hidden_next, "fa000002"
      refute_includes hidden_next, "fa000003"
      assert_includes hidden_next, "fa000007", "open descendant hoists through a closed ancestor"
      shown_next = tree_rows(store, :next, show_deferred: true).filter_map { |row| row.item&.id }
      assert_equal %w[fa000002 fa000003], shown_next & %w[fa000002 fa000003],
                   "a revealed unavailable Next anchor carries its nonmatching rider"

      hidden_agenda = tree_rows(store, :agenda).filter_map { |row| row.item&.id }
      assert_equal %w[fa000008 fa000009], hidden_agenda,
                   "an available dated anchor carries its undated agenda rider"
      shown_inbox = tree_rows(store, :inbox, show_deferred: true).filter_map { |row| row.item&.id }
      assert_equal %w[fa000004 fa000005], shown_inbox,
                   "a revealed held Inbox anchor carries its nonmatching rider"

      [false, true].each do |revealed|
        rows = V.rows(
          :projects, store.items, tree: store.tree, today: TODAY,
          show_deferred: revealed, store: store
        )
        header = rows.find { |row| !row.item && !row.node && A.strip(row.text).start_with?("Work") }
        refute_nil header
        assert_equal rows.count(&:item), A.strip(header.text)[/(\d+) open/, 1].to_i,
                     "project header count matches the same hidden/revealed body"
      end
    end
  end

  # Hierarchy remains a presentation concern: an agenda anchor brings its open,
  # undated descendants along for context, while flat/filter mode stays a strict
  # list of dated query matches.
  def test_tree_contextual_riders_are_an_intentional_output_difference
    with_records(NESTED) do |store|
      flat_ids = V.rows(:agenda, store.items, today: TODAY)
                  .filter_map { |row| row.item&.id }.to_set
      tree_ids = tree_rows(store, :agenda).filter_map { |row| row.item&.id }.to_set

      assert_equal Set["p1", "c1"], flat_ids
      assert_equal Set["g1", "c2"], tree_ids - flat_ids,
                   "undated ancestors/descendants ride the matching dated subtree"
    end
  end

  def test_project_grouping_is_identical_in_flat_and_tree_modes
    with_records(PROJ_DEFERRED) do |store|
      flat = V.rows(:projects, store.items, show_deferred: true, today: TODAY, store: store)
      tree = V.rows(:projects, store.items, tree: store.tree, show_deferred: true,
                                     today: TODAY, store: store)
      assert_equal ["Work"], project_header_titles(flat)
      assert_equal project_header_titles(flat), project_header_titles(tree)
    end
  end

  def test_project_group_order_uses_earliest_descendant_date_in_both_modes
    records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "s1", "title" => "Alpha" },
      { "type" => "task", "id" => "p1", "parent" => "s1", "state" => "TODO", "title" => "parent" },
      { "type" => "task", "id" => "c1", "parent" => "p1", "state" => "NEXT", "title" => "dated child",
        "deadline" => "2026-07-02" },
      { "type" => "section", "id" => "s2", "title" => "Beta" },
      { "type" => "task", "id" => "p2", "parent" => "s2", "state" => "NEXT", "title" => "later root",
        "deadline" => "2026-07-03" },
    ]
    with_records(records) do |store|
      flat = V.rows(:projects, store.items, today: TODAY, store: store)
      tree = V.rows(:projects, store.items, tree: store.tree, today: TODAY, store: store)
      assert_equal %w[Alpha Beta], project_header_titles(flat)
      assert_equal project_header_titles(flat), project_header_titles(tree)
    end
  end

  def test_agenda_tree_orders_a_dated_anchor_by_its_earlier_visible_descendant
    records = [
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "ad000001", "title" => "Work" },
      { "type" => "task", "id" => "ad000002", "parent" => "ad000001", "state" => "TODO",
        "title" => "Later dated parent", "deadline" => "2026-07-30" },
      { "type" => "task", "id" => "ad000003", "parent" => "ad000002", "state" => "NEXT",
        "title" => "Earlier dated child", "deadline" => "2026-07-15" },
      { "type" => "task", "id" => "ad000004", "parent" => "ad000001", "state" => "NEXT",
        "title" => "Middle root", "deadline" => "2026-07-20" },
    ]
    with_records(records) do |store|
      ids = tree_rows(store, :agenda).filter_map { |row| row.item&.id }
      assert_equal %w[ad000002 ad000003 ad000004], ids
    end
  end

  def test_badges_are_shared_between_flat_and_tree_renderers
    records = NESTED.map(&:dup)
    ship = records.find { |record| record["id"] == "p1" }
    ship["tags"] = %w[@computer defer]
    ship["recur"] = ".+1w"
    with_records(records) do |store|
      flat = V.rows(:agenda, store.items, show_deferred: true, today: TODAY)
              .find { |row| row.item&.id == "p1" }
      tree = V.rows(:agenda, store.items, tree: store.tree, show_deferred: true,
                                    today: TODAY)
              .find { |row| row.item&.id == "p1" }
      [flat, tree].each do |row|
        assert_includes A.strip(row.text), "↻"
        assert_includes A.strip(row.text), "⏸"
      end
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
    assert_equal 1, rs.size # future Available from rows stay unavailable
    assert_includes rs[0].text, "Book flight"
    assert_includes rs[0].text, "DUE"
    assert rs.all?(&:item), "agenda rows are all selectable"

    with_store do |store, _o, _a|
      revealed = V.rows(:agenda, store.items, today: TODAY, show_deferred: true)
      self_eval = revealed.find { |row| row.item&.title&.include?("self-eval") }
      assert_includes self_eval.text, "AVL"
      assert_includes A.strip(self_eval.text), "⏳ 7/3"
    end
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
      refute rs.any? { |t| t.include?("epsilon") }, "future Available from is unavailable"
      assert rs[q3...q4].any? { |t| t.include?("gamma") },   "C + near deadline → Q3"
      assert rs[q4..].any?   { |t| t.include?("delta") },    "far deadline → Q4"

      revealed = texts(V.rows(:quadrants, store.items, today: TODAY, urgent_days: 3,
                                                   show_deferred: true))
      rq2 = revealed.index { |t| t.start_with?("Q2") }
      rq3 = revealed.index { |t| t.start_with?("Q3") }
      assert revealed[rq2...rq3].any? { |t| t.include?("epsilon") },
             "revealed future Available from remains important but not urgent"
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

  def test_projects_view_groups_open_tasks_by_project
    with_store do |store, _o, _a|
      rs = V.rows(:projects, store.items, today: TODAY, store: store)
      stripped = texts(rs)
      work = stripped.index { |t| t.start_with?("Work") }
      home = stripped.index { |t| t.start_with?("Home") }
      assert work, "Work project header is shown"
      assert home, "Home project header is shown"
      assert_includes stripped[work], "3 open"
      assert_includes stripped[work], "2 next"
      assert stripped[work...home].any? { |t| t.include?("Book flight") }
      assert stripped[home..].any? { |t| t.include?("Water the plants") }
      refute stripped.any? { |t| t.start_with?("Inbox") }
      assert rs.any? { |r| r.item&.title == "Book flight in Concur" }, "project task rows stay selectable"
    end
  end

  # A DONE parent is never an active project header: its open (hoisted) child
  # groups under the nearest open ancestor — here the enclosing section — and no
  # "done middle" header appears, matching the outliner's hoisting through closed
  # ancestors.
  PROJ_DONE_PARENT = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "s1", "title" => "Work" },
    { "type" => "task", "id" => "done1", "parent" => "s1", "state" => "DONE",
      "title" => "done middle", "closed" => "2026-06-01" },
    { "type" => "task", "id" => "oc", "parent" => "done1", "state" => "NEXT",
      "title" => "hoisted child" },
  ].freeze

  def test_projects_skips_done_parent_groups_under_open_ancestor
    with_records(PROJ_DONE_PARENT) do |store|
      stripped = texts(V.rows(:projects, store.items, today: TODAY, store: store))
      refute stripped.any? { |t| t.start_with?("done middle") },
             "a DONE parent must not head an active project group"
      work = stripped.index { |t| t.start_with?("Work") }
      assert work, "child groups under its nearest open ancestor (the section)"
      assert stripped[work..].any? { |t| t.include?("hoisted child") }
    end
  end

  # No open ancestor at all (a DONE root task, no section above): the hoisted
  # child has no project and drops out of the Projects view entirely — like a
  # bare top-level task. It stays visible in the other views.
  PROJ_ORPHAN = [
    { "type" => "meta", "version" => 1 },
    { "type" => "task", "id" => "done1", "state" => "DONE",
      "title" => "done root", "closed" => "2026-06-01" },
    { "type" => "task", "id" => "oc", "parent" => "done1", "state" => "NEXT",
      "title" => "orphan child" },
  ].freeze

  def test_projects_excludes_task_with_no_open_ancestor
    with_records(PROJ_ORPHAN) do |store|
      stripped = texts(V.rows(:projects, store.items, today: TODAY, store: store))
      refute stripped.any? { |t| t.include?("orphan child") },
             "a task under only closed ancestors has no project"
      # it's still reachable in a normal view
      assert tree_texts(store, :next).any? { |t| t.include?("orphan child") },
             "the hoisted child still shows in Next"
    end
  end

  # A deferred parent remains an open task, but it is not a project heading.
  # Flat and tree modes both group it and its child under the enclosing SECTION
  # instead of promoting the parent task to a pseudo-project.
  PROJ_DEFERRED = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "s1", "title" => "Work" },
    { "type" => "task", "id" => "dp", "parent" => "s1", "state" => "TODO",
      "title" => "deferred project", "tags" => %w[defer] },
    { "type" => "task", "id" => "kid", "parent" => "dp", "state" => "NEXT",
      "title" => "child of deferred" },
  ].freeze

  def test_projects_deferred_parent_stays_under_enclosing_section
    with_records(PROJ_DEFERRED) do |store|
      hidden = V.rows(:projects, store.items, today: TODAY, store: store)
      refute hidden.any?(&:item), "deferred parent hides its descendant in flat mode"

      rows = V.rows(:projects, store.items, show_deferred: true, today: TODAY, store: store)
      assert_equal ["Work"], project_header_titles(rows)
      stripped = texts(rows)
      assert stripped.any? { |t| t.include?("deferred project") }
      assert stripped.any? { |t| t.include?("child of deferred") }
    end
  end

  # -- Projects view, tree mode --------------------------------------------

  # Tree-mode Projects rows (nesting-aware body, unlike the flat `/`-filter path).
  def projects_rows(store, collapsed: Set.new, show_deferred: false)
    V.rows(:projects, store.items, tree: store.tree, collapsed: collapsed,
                                   show_deferred: show_deferred, today: TODAY, store: store)
  end

  # A project with a parent task and its own child, plus a leaf sibling.
  PROJ_NESTED = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "s1", "title" => "Work" },
    { "type" => "task", "id" => "p1", "parent" => "s1", "state" => "NEXT",
      "title" => "parent task", "deadline" => "2026-07-03" },
    { "type" => "task", "id" => "c1", "parent" => "p1", "state" => "TODO",
      "title" => "child task" },
    { "type" => "task", "id" => "l1", "parent" => "s1", "state" => "TODO",
      "title" => "leaf sibling" },
  ].freeze

  def test_projects_tree_renders_child_directly_below_parent
    with_records(PROJ_NESTED) do |store|
      rs = projects_rows(store)
      pi = rs.index { |r| A.strip(r.text).include?("parent task") }
      ci = rs.index { |r| A.strip(r.text).include?("child task") }
      refute_nil pi
      assert_equal pi + 1, ci, "child renders on the row immediately after its parent (DFS)"
      assert_operator rs[ci].node.level, :>, rs[pi].node.level, "child nests deeper"
      assert_includes rs[ci].text, "│", "nested child row carries the thread glyph"
    end
  end

  def test_projects_tree_bolds_containers_not_leaves
    with_records(PROJ_NESTED) do |store|
      rs = projects_rows(store)
      parent = rs.find { |r| A.strip(r.text).include?("parent task") }
      child  = rs.find { |r| A.strip(r.text).include?("child task") }
      leaf   = rs.find { |r| A.strip(r.text).include?("leaf sibling") }
      bold = Tui::Theme.sgr(:outline_container) # "\e[1m" by default
      refute bold.empty?, "outline_container must carry an SGR for this test"
      assert_includes parent.text, bold, "a parent (container) row is bolded"
      refute_includes child.text, bold, "a leaf child row is not bolded"
      refute_includes leaf.text, bold, "a leaf sibling row is not bolded"
    end
  end

  # tree: nil is the flat fallback the `/` filter path uses: every descendant
  # flattened, no markers, no per-row node, fixed 2-space indent.
  def test_projects_flat_fallback_matches_legacy_shape
    with_records(PROJ_NESTED) do |store|
      flat = V.rows(:projects, store.items, today: TODAY, store: store)
      flat.each do |r|
        assert_nil r.node, "flat projects row carries no node"
        refute_includes r.text, "│", "flat projects has no thread glyph"
        refute_includes r.text, "▾"
        refute_includes r.text, "▸"
      end
      stripped = flat.map { |r| A.strip(r.text) }
      assert stripped.any? { |t| t.start_with?("Work") }, "project header present"
      assert stripped.any? { |t| t.include?("parent task") }
      assert stripped.any? { |t| t.include?("child task") }
    end
  end

  # A project SUB-HEADING nested inside another section (Projects -> "Launch
  # the site" -> tasks) — mirrors docs/conventions.md's own canonical example.
  # top_level_task_nodes previously unwrapped only one level of sections, so
  # every task under a nested project heading was invisible to EVERY tree view
  # (agenda/next/quadrants/inbox/projects all seed anchors from it) — a
  # pre-existing bug, not introduced by the nesting-visuals change, but one
  # this change's Projects rewrite would otherwise have inherited/surfaced.
  PROJ_DOUBLE_NESTED = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "s1", "title" => "Projects" },
    { "type" => "section", "id" => "s2", "parent" => "s1", "title" => "Launch the site" },
    { "type" => "task", "id" => "t1", "parent" => "s2", "state" => "NEXT",
      "title" => "pick a generator", "deadline" => "2026-07-03" },
  ].freeze

  def test_task_under_nested_project_heading_is_not_dropped
    with_records(PROJ_DOUBLE_NESTED) do |store|
      # agenda (it's dated) / next (it's NEXT) / quadrants (state-agnostic) all
      # source their anchors from the same top_level_task_nodes walk; inbox
      # only shows INBOX-state items so a NEXT task doesn't apply there.
      %i[agenda next quadrants].each do |view|
        texts = tree_texts(store, view)
        assert texts.any? { |t| t.include?("pick a generator") },
               "#{view} tree view must surface a task under a nested project heading"
      end
      rs = projects_rows(store)
      stripped = rs.map { |r| A.strip(r.text) }
      assert stripped.any? { |t| t.start_with?("Launch the site") }, "nested project header present"
      assert stripped.any? { |t| t.include?("pick a generator") }
    end
  end

  # Project header rows carry neither an item nor a node and aren't blank; the
  # title is the leading token before the "  N open" stats. Extracting them lets
  # a test assert the EXACT set of project groups rendered.
  def project_header_titles(rs)
    rs.reject { |r| r.item || r.node }
      .map { |r| A.strip(r.text) }
      .reject(&:empty?)
      .map { |t| t.sub(/  \d+ open.*\z/, "") }
  end

  # Bug 1 regression: a task with a nested open child (Work → "parent task"
  # (NEXT) → "child task" (TODO)) must NOT spawn a spurious project header named
  # after the parent task. The old code grouped the child by open_project (its
  # nearest OPEN ancestor = "parent task"), so groups gained a bogus "parent
  # task" key with an empty body — a dangling header. Grouping by the enclosing
  # SECTION instead lands the child under "Work" (nested beneath its parent), so
  # the ONLY header is "Work".
  def test_projects_tree_no_spurious_header_for_intermediate_task
    with_records(PROJ_NESTED) do |store|
      rs = projects_rows(store)
      assert_equal ["Work"], project_header_titles(rs),
                   "only the enclosing section heads a group — no pseudo-project per open parent task"
    end
  end

  # A project with one live task and one deferred task.
  PROJ_MIXED_DEFER = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "s1", "title" => "Work" },
    { "type" => "task", "id" => "t1", "parent" => "s1", "state" => "TODO",
      "title" => "live task", "deadline" => "2026-07-03" },
    { "type" => "task", "id" => "t2", "parent" => "s1", "state" => "TODO",
      "title" => "someday task", "tags" => %w[defer] },
  ].freeze

  # A project whose every open task is deferred.
  PROJ_ALL_DEFERRED = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "s1", "title" => "Work" },
    { "type" => "task", "id" => "t1", "parent" => "s1", "state" => "TODO",
      "title" => "someday one", "tags" => %w[defer] },
    { "type" => "task", "id" => "t2", "parent" => "s1", "state" => "TODO",
      "title" => "someday two", "tags" => %w[defer] },
  ].freeze

  # Bug 2 regression: header stats must be computed from the SAME (deferred-
  # respecting) traversal as the body, so the stated open-count always matches
  # the actual number of body rows. The old code counted ALL open items
  # (deferred included) for the header but hid deferred tasks from the body when
  # show_deferred was false — an overcount with no matching row.
  def test_projects_tree_header_count_matches_body_when_deferred_hidden
    with_records(PROJ_MIXED_DEFER) do |store|
      rs = projects_rows(store, show_deferred: false)
      header = rs.find { |r| r.item.nil? && r.node.nil? && A.strip(r.text).start_with?("Work") }
      refute_nil header, "Work project header present"
      open_count = A.strip(header.text)[/(\d+) open/, 1].to_i
      body_rows = rs.count { |r| r.item }
      assert_equal 1, body_rows, "only the live task renders (deferred hidden)"
      assert_equal body_rows, open_count, "header open-count matches rendered body rows"
    end
  end

  def test_projects_tree_header_count_matches_body_when_deferred_shown
    with_records(PROJ_MIXED_DEFER) do |store|
      rs = projects_rows(store, show_deferred: true)
      header = rs.find { |r| r.item.nil? && r.node.nil? && A.strip(r.text).start_with?("Work") }
      refute_nil header
      open_count = A.strip(header.text)[/(\d+) open/, 1].to_i
      body_rows = rs.count { |r| r.item }
      assert_equal 2, body_rows, "both tasks render when deferred shown"
      assert_equal body_rows, open_count, "header open-count matches rendered body rows"
      assert rs.any? { |r| r.item && A.strip(r.text).include?("someday task") },
             "the deferred task appears in the body when show_deferred"
    end
  end

  # Bug 2, degenerate case: a project whose only open tasks are all deferred must
  # NOT print a bare header (with zero body rows) when show_deferred is false —
  # it should vanish entirely, exactly like a project with no anchors.
  def test_projects_tree_all_deferred_project_hidden_when_deferred_hidden
    with_records(PROJ_ALL_DEFERRED) do |store|
      hidden = projects_rows(store, show_deferred: false)
      refute hidden.any? { |r| A.strip(r.text).start_with?("Work") },
             "an all-deferred project shows no dangling header when deferred hidden"

      shown = projects_rows(store, show_deferred: true)
      assert shown.any? { |r| A.strip(r.text).start_with?("Work") },
             "the project reappears once deferred tasks are revealed"
      assert_equal 2, shown.count { |r| r.item }, "both deferred tasks render when shown"
    end
  end

  # The detail-panel project string follows the same nearest-open-ancestor rule
  # as the Projects view — one definition (Node#open_project), so a closed
  # parent is skipped in the panel too.
  def test_open_project_skips_closed_parent_for_detail
    with_records(PROJ_DONE_PARENT) do |store|
      child = store.node_for(store.items.find { |i| i.title == "hoisted child" })
      assert_equal "Work", child.open_project.title, "skips the DONE parent up to the section"
    end
  end

  # List rows paint their fields through theme slots (contexts via :context).
  # Parentage is shown by the outliner's nesting and by the dedicated Projects
  # view rather than an inline project tag on every row, so a list task row
  # carries its themed contexts and stays selectable.
  def test_task_rows_theme_context_fields
    with_store do |store, _o, _a|
      row = V.rows(:agenda, store.items, today: TODAY, store: store)
             .find { |r| r.item&.title&.include?("Book flight") }
      assert_includes row.text, "\e[1;36m@computer\e[0m"   # context painted with :context slot
      assert_equal "Book flight in Concur", row.item.title, "task row stays selectable"
    end
  end

  def test_done_items_never_appear_in_open_views
    %i[agenda next quadrants inbox projects].each do |view|
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
                  "alpha hoisted next", "beta todo", "gamma inbox", "gamma sub inbox"]
      all_open.each { |t| assert_equal 1, count(quad, t), "#{t} once in quadrants" }
      ["gamma sub todo", *hidden].each { |t| assert_equal 0, count(quad, t), "#{t} absent from quadrants" }

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
      ["gamma inbox", "gamma sub inbox"].each do |t|
        assert_equal 1, count(inb, t), "#{t} once in inbox"
      end
      ["alpha next", "beta todo", "gamma sub todo", *hidden].each do |t|
        assert_equal 0, count(inb, t), "#{t} not in inbox"
      end

      # agenda: every open task belonging to a dated-containing subtree, once.
      ag = tree_texts(store, :agenda)
      ["alpha next", "alpha sub todo", "alpha sub next", "alpha hoisted todo"].each do |t|
        assert_equal 1, count(ag, t), "#{t} once in agenda"
      end
      # undated leaves with no dated descendant don't reach the agenda.
      ["alpha hoisted next", "beta todo", "gamma inbox", "gamma sub todo", "gamma sub inbox", *hidden].each do |t|
        assert_equal 0, count(ag, t), "#{t} not in agenda"
      end
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
