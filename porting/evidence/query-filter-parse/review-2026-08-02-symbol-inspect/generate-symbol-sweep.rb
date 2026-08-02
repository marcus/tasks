# The exhaustive Symbol#inspect sweep that found the three defects of the
# 84df4c5 source-fidelity review. Every non-surrogate codepoint is used as an
# unknown constructor keyword, 256 per case, so 1,112,064 names cost 4,344
# cases. `sigil` prefixes each name, which is how the `$`/`@`/`@@` branches of
# query.bareSymbolName are reached.
#
#   ruby generate-symbol-sweep.rb OUT.jsonl [sigil]
#
# Compare structurally, never byte-wise: Ruby's JSON.generate emits U+2028 and
# U+2029 raw where Go's encoder escapes them, for the same decoded value.
require "json"

out = ARGV.fetch(0)
sigil = ARGV.fetch(1, "")

codepoints = (0..0x10FFFF).reject { |cp| cp >= 0xD800 && cp <= 0xDFFF }

File.open(out, "w") do |file|
  codepoints.each_slice(256).with_index do |chunk, index|
    keywords = {}
    chunk.each { |cp| keywords[sigil + cp.chr(Encoding::UTF_8)] = 1 }
    file.puts JSON.generate({ "case_id" => "sym-#{index}", "operation" => "new",
                              "kwargs" => keywords })
  end
end

warn "#{codepoints.length} names in #{(codepoints.length / 256.0).ceil} cases"
