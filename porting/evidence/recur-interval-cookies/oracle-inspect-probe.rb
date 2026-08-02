# Ruby oracle capture for the rejection/echo spelling of Recur.parse_result and
# Recur.next_date. Run from the repository root:
#
#   ruby porting/evidence/recur-interval-cookies/oracle-inspect-probe.rb
#
# Every case is an input Recur rejects, so the observable output is the error
# string — which embeds the caller's spelling through String#inspect. Inputs are
# written with explicit escapes so the file stays plain ASCII and no editor can
# silently rewrite a control or format character.
$LOAD_PATH.unshift(File.expand_path("../../../lib", __dir__))
require "tasks/recur"
require "date"

CASES = {
  "plain"          => "zz",
  "dquote"         => "he said \"weekly\"",
  "backslash"      => "back\\slash",
  "tab"            => "no\tcookie",
  "newline"        => "no\ncookie",
  "carriage"       => "no\rcookie",
  "escape"         => "\e[0mzz",
  "bell"           => "zz\a",
  "vtab"           => "zz\vzz",
  "backspace"      => "zz\bzz",
  "formfeed"       => "zz\fzz",
  "nul"            => "zz\u0000zz",
  "soh"            => "zz\u0001",
  "us"             => "zz\u001f",
  "del"            => "zz\u007f",
  "c1_80"          => "zz\u0080",
  "c1_9f"          => "zz\u009f",
  "interp_brace"   => "zz\#{1}",
  "interp_dollar"  => "zz\#$x",
  "interp_at"      => "zz\#@x",
  "hash_plain"     => "zz#zz",
  "hash_trailing"  => "zz#",
  "nbsp"           => "zz\u00a0zz",
  "accent"         => "wöchentlich",
  "cjk"            => "毎週",
  "emoji"          => "\u{1f600}",
  "soft_hyphen"    => "zz\u00adzz",
  "zwsp"           => "zz\u200bzz",
  "bom"            => "zz\ufeffzz",
  "combining"      => "z\u0301z",
  "line_sep"       => "zz\u2028zz",
  "para_sep"       => "zz\u2029zz",
  "unassigned"     => "zz\u0378",
  "private_use"    => "zz\u{e000}",
  "supp_private"   => "zz\u{f0000}",
  "tag_char"       => "zz\u{e0001}",
  "musical_ctl"    => "zz\u{1d173}",
  "high_unassign"  => "zz\u{10ffff}",
  "supp_unassign"  => "zz\u{2ffff}",
}

puts "== inputs (hex of UTF-8 bytes) =="
CASES.each { |name, input| puts "input.#{name}\t#{input.b.unpack1('H*')}" }

puts "== Recur.parse_result errors =="
CASES.each do |name, input|
  result = begin
    Tasks::Recur.parse_result(input)[:error] || "<accepted>"
  rescue StandardError => e
    "<#{e.class}: #{e.message}>"
  end
  puts "parse.#{name}\t#{result}"
end

puts "== Recur.next_date ArgumentError messages =="
CASES.each do |name, input|
  message = begin
    Tasks::Recur.next_date(input, from: Date.new(2026, 1, 1), today: Date.new(2026, 1, 1))
    "<no raise>"
  rescue StandardError => e
    e.message
  end
  puts "next_date.#{name}\t#{message}"
end

# Bytes that are not valid UTF-8 cannot be written as a Ruby literal in this
# file without an encoding pragma, so they are built from their hex spelling.
INVALID = {
  "lone_ff"      => "ff",
  "truncated_e6" => "7a7ae6",
  "bare_80"      => "7a7a80",
}

puts "== invalid UTF-8 bytes =="
INVALID.each do |name, hex|
  input = [hex].pack("H*").force_encoding(Encoding::UTF_8)
  puts "input.invalid.#{name}\t#{hex}"
  parsed = begin
    Tasks::Recur.parse_result(input)[:error] || "<accepted>"
  rescue StandardError => e
    "<#{e.class}: #{e.message}>"
  end
  puts "parse.invalid.#{name}\t#{parsed}"
  raised = begin
    Tasks::Recur.next_date(input, from: Date.new(2026, 1, 1), today: Date.new(2026, 1, 1))
    "<no raise>"
  rescue StandardError => e
    e.message
  end
  puts "next_date.invalid.#{name}\t#{raised}"
end
