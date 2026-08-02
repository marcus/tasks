# Adversarial Float#to_s corpus for the query-filter-parse source-fidelity
# review at 84df4c5. Each case feeds one JSON number literal through the
# `text` collection, which is where Kernel#Array + to_s renders it.
require "json"

literals = []

# Targeted layout boundaries: the decimal point at, around, and past the
# DBL_DIG (15) and 16-digit rules, and around the 0.000ddd / exponent cut.
%w[1 12 123 1234567 123456789012345 1234567890123456 12345678901234567
   1000000000000000 10000000000000000 9999999999999999 99999999999999999
   1234567890123456789012345].each do |digits|
  (-30..30).each do |shift|
    literals << "#{digits}e#{shift}"
    literals << "-#{digits}e#{shift}"
  end
end

# Fixed-notation literals across the same boundaries, written without an
# exponent so the parser sees a different literal shape for the same value.
(1..25).each do |before|
  (0..4).each do |after|
    whole = (1..before).map { |i| ((i % 9) + 1).to_s }.join
    frac = after.zero? ? "0" : (1..after).map { |i| ((i % 9) + 1).to_s }.join
    literals << "#{whole}.#{frac}"
    literals << "-#{whole}.#{frac}"
  end
end

# The 0.000ddd / exponent boundary: point in 0, -1, -2, -3, -4, -5.
(0..8).each do |zeros|
  %w[1 15 105 123456789012345678].each do |digits|
    literals << "0.#{"0" * zeros}#{digits}"
    literals << "-0.#{"0" * zeros}#{digits}"
  end
end

# Integer literals, including the -0 renormalisation and widths past int64.
literals.concat(["0", "-0", "1", "-1", "9223372036854775807", "9223372036854775808",
                 "-9223372036854775808", "-9223372036854775809",
                 "170141183460469231731687303715884105728",
                 "-170141183460469231731687303715884105728"])

# Exponent-form spellings the parser must still treat as Float.
literals.concat(["1E5", "1e+5", "1e-5", "0e0", "-0e0", "0.0", "-0.0",
                 "1.0e400", "-1.0e400", "1e-400", "-1e-400", "5e-324", "-5e-324",
                 "1.7976931348623157e308", "2.2250738585072014e-308"])

# A deterministic sweep of random doubles, rendered by Ruby's own shortest
# repr so the literal round-trips, plus the same value shifted by powers.
random = Random.new(20260802)
40_000.times do
  bits = random.rand(1 << 64)
  value = [bits].pack("Q>").unpack1("G")
  next unless value.finite?

  literals << value.to_s
end

literals.uniq!

File.open(ARGV.fetch(0), "w") do |file|
  literals.each_with_index do |literal, index|
    file.puts(%({"case_id":"float-#{index}","operation":"new","kwargs":{"text":[#{literal}]}}))
  end
end

warn "#{literals.length} float literals"
