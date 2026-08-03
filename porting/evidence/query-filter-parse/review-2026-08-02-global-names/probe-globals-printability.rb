# Does the `$-X` branch still require X to be printable?
[0x0086, 0x009F, 0x00A0, 0x0378, 0x0085, 0x200B, 0xE0001].each do |code|
  bare = "$-#{[code].pack("U")}"
  solo = [code].pack("U")
  puts format("U+%04X  dash=%-14s solo=%s", code,
              bare.to_sym.inspect.dump, solo.to_sym.inspect.dump)
end
