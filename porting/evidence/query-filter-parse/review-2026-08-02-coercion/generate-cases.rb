require "json"

def chr(cp) = cp.chr(Encoding::UTF_8)

cases = [
  { "case_id" => "rev-hash-key-order", "operation" => "new",
    "kwargs" => { "contexts" => [{ "b" => 1, "a" => 2 }] } },
  { "case_id" => "rev-top-level-hash-order", "operation" => "new",
    "kwargs" => { "tags" => { "z" => 1, "a" => 2 } } },
  { "case_id" => "rev-float-exponent", "operation" => "new",
    "kwargs" => { "contexts" => JSON.parse("[100.0, 1e2, 1.0E-5, 2.5e10]") } },
  { "case_id" => "rev-big-integer", "operation" => "new",
    "kwargs" => { "tags" => JSON.parse("[12345678901234567890]") } },
  { "case_id" => "rev-inspect-control", "operation" => "new",
    "kwargs" => { "contexts" => [[chr(0x01)]] } },
  { "case_id" => "rev-inspect-c1", "operation" => "new",
    "kwargs" => { "tags" => [[chr(0x85)]] } },
  { "case_id" => "rev-inspect-del", "operation" => "new",
    "kwargs" => { "text" => [[chr(0x7f)]] } },
  { "case_id" => "rev-inspect-unassigned", "operation" => "new",
    "kwargs" => { "contexts" => [[chr(0x378)]] } },
]

File.open("/tmp/qfp-ses5d055f/adversarial.jsonl", "w") do |f|
  cases.each { |c| f.puts(JSON.generate(c)) }
end
