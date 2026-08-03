# Reproduction corpus for the 84df4c5 source-fidelity review of
# query-filter-parse. Every case names an unknown constructor keyword, which is
# the observable that renders through query.InspectSymbol (Ruby Symbol#inspect).
require "json"

cases = []
add = lambda do |id, names|
  kw = {}
  names.each { |n| kw[n] = 1 }
  cases << { "case_id" => id, "operation" => "new", "kwargs" => kw }
end
chr = ->(cp) { cp.chr(Encoding::UTF_8) }

# Finding 1 - Symbol#inspect quotes C0 and DEL with \xNN, not String#inspect's
# \uNNNN. All 25 codepoints that have no named escape.
c0 = ((0x00..0x1F).to_a + [0x7F]) - [0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x1B]
c0.each { |cp| add.call("sym-c0-%02X" % cp, [chr.call(cp)]) }
add.call("sym-c0-embedded", ["a" + chr.call(0x01), chr.call(0x7F) + "z"])
# The named escapes must keep sharing String#inspect's spelling.
add.call("sym-named-escapes", [0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x1B].map(&chr))

# Finding 2 - a non-printable non-ASCII codepoint is not an identifier
# character: Ruby quotes and escapes it where Go prints it bare.
[0x0080, 0x0081, 0x009F, 0x00A0, 0x2028, 0x2029, 0x202A, 0xFFFE, 0x10FFFF,
 0xE0002].each { |cp| add.call("sym-nonprintable-%04X" % cp, [chr.call(cp)]) }
add.call("sym-nonprintable-embedded",
         ["a" + chr.call(0x0080), chr.call(0x2028) + "z", "ok" + chr.call(0xFFFE) + "nope"])
# U+0085 is the one codepoint String#inspect escapes that a symbol still prints
# bare, so the correction is not simply "gate on rubyPrintable".
add.call("sym-nel-bare", [chr.call(0x85), "a" + chr.call(0x85), chr.call(0x85) + "a"])
# Printable non-ASCII stays bare, including above the BMP and the format
# characters Ruby prints raw.
add.call("sym-printable-nonascii",
         [0x2192, 0x2460, 0x00E9, 0x3042, 0x1F600, 0x00AD, 0x200B, 0xFEFF, 0x1D173].map(&chr))

# Finding 3 - the single-character global fallback is a fixed vocabulary, not
# "any one character". Ruby rejects these twelve.
" #%()-[]^{|}".each_char { |c| add.call("sym-global-reject-%02X" % c.ord, ["$#{c}"]) }
# ...and accepts these twenty, so the correction must not over-reject.
["!", "\"", "$", "&", "'", "*", "+", ",", ".", "/", ":", ";", "<", "=", ">",
 "?", "@", "\\", "`", "~"].each { |c| add.call("sym-global-accept-%02X" % c.ord, ["$#{c}"]) }
add.call("sym-global-digits", ["$1", "$0", "$99", "$_", "$foo", "$"])
add.call("sym-global-nonprintable",
         ["$" + chr.call(0x80), "$" + chr.call(0x85), "$" + chr.call(0x2192)])

# Regression guards for the shapes earlier reviews already pinned.
add.call("sym-operators", ["+", "-", "**", "<=>", "[]", "[]=", "+@", "-@", "`", "!", "~"])
add.call("sym-quoted-operators", ["~@", "!@", "&&", "||", "="])
add.call("sym-ivars", ["@a", "@@a", "@1", "@@1", "@", "@@"])
add.call("sym-trailing", ["a?", "a!", "a=", "a?=", "a!!", "a=="])

File.open(ARGV.fetch(0), "w") { |f| cases.each { |c| f.puts JSON.generate(c) } }
warn "#{cases.length} cases"
