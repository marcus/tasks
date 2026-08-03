# Differential driver: apply ONE field change through Tasks::Store and print
# the typed outcome. The store bytes and the journal are the other half of the
# comparison and are left on disk for the caller to diff.
#
# Fields with a Ruby field baseline go through patch_task! (the field-scoped
# conflict check). `activate` has none — EditSnapshot#expected_for raises for
# it — so it goes through apply_changeset!, which is the only route the Ruby
# product itself uses for that field.
$LOAD_PATH.unshift(File.expand_path(ARGV[1]))
require "json"
require "date"
require "tasks/store"
require "tasks/task_patch"
require "tasks/task_changeset"
require "tasks/temporal_value"
require "tasks/temporal_context"

spec = JSON.parse(File.read(ARGV[0]))

def decode(value)
  case value["kind"]
  when "none" then nil
  when "text" then value["text"]
  when "bool" then value["bool"]
  when "list" then value["list"]
  when "tag_delta" then { "add" => value["add"], "remove" => value["remove"] }
  when "temporal", "date"
    Tasks::TemporalValue.new(date: Date.iso8601(value["date"]),
                             local_time: value["local"], timezone: value["timezone"],
                             fold: value["fold"] || 0, validate: false)
  end
end

now = Time.parse(spec["now"]).utc
store = Tasks::Store.new(
  org: spec["org"], archive: spec["archive"], journal_dir: spec["journal"],
  coalesce_scope: "pinned-scope", now: -> { now }, device: spec["device"],
  id_source: -> { "aaaaaaaa" }
)
today = Date.iso8601(spec["today"])
field = spec["field"].to_sym
value = decode(spec["value"])
label = spec["label"].to_s.empty? ? nil : spec["label"]

snapshot = store.edit_snapshot(spec["id"])
verb = spec["verb"]
result =
  if spec["changes"]
    changes = spec["changes"].each_with_object({}) do |item, out|
      out[item["field"].to_sym] = decode(item["value"])
    end
    changeset = Tasks::TaskChangeset.from(snapshot, changes: changes, history_label: label)
    store.apply_changeset!(changeset, today: today)
  elsif verb == "undelegate"
    store.undelegate_task!(spec["id"])
  elsif verb == "release"
    store.release_task!(spec["id"], worker: spec["worker"], force: spec["force"] == true)
  elsif verb == "work_ref"
    store.set_work_ref!(spec["id"], spec["work_ref"], worker: spec["worker"])
  elsif snapshot.nil?
    # An invalid store has no snapshot; the transaction still has to answer.
    store.patch_task!(Tasks::TaskPatch.new(id: spec["id"], field: field, value: value,
                                           expected: nil, history_label: label), today: today)
  elsif field == :activate
    changeset = Tasks::TaskChangeset.from(snapshot, changes: { activate: value },
                                          history_label: label)
    store.apply_changeset!(changeset, today: today)
  else
    expected =
      case field
      when :tag_delta then snapshot.metadata.fetch(:tag_sequence)
      when :date_clear then snapshot.metadata.fetch(:date_state)
      else snapshot.expected_for(field)
      end
    patch = Tasks::TaskPatch.new(id: spec["id"], field: field, value: value,
                                 expected: expected, history_label: label)
    store.patch_task!(patch, today: today)
  end

puts JSON.generate({ "status" => result.status.to_s, "errors" => result.errors,
                     "rolled_back" => result.rolled_back? })
