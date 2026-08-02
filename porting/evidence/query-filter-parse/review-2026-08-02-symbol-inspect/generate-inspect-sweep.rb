# Exhaustive String#inspect corpus, generated independently of the repair's
# own harness. Every non-surrogate codepoint is nested one Array deep inside
# `text`, which is the only path that reaches inspectString: a bare String
# element renders through to_s, an element inside an Array through inspect.
require "json"

CHUNK = 512

codepoints = (0..0x10FFFF).reject { |cp| cp >= 0xD800 && cp <= 0xDFFF }

File.open(ARGV.fetch(0), "w") do |file|
  codepoints.each_slice(CHUNK).with_index do |chunk, index|
    strings = chunk.map { |cp| cp.chr(Encoding::UTF_8) }
    file.puts(JSON.generate({ "case_id" => "inspect-#{index}", "operation" => "new",
                              "kwargs" => { "text" => [strings] } }))
  end
end

warn "#{codepoints.length} codepoints in #{(codepoints.length / CHUNK.to_f).ceil} cases"
