File.open("/tmp/qfp-ses5d055f/ruby-inspect.txt","w") do |f|
  (0..0x10FFFF).each do |cp|
    next if cp >= 0xD800 && cp <= 0xDFFF
    s = cp.chr(Encoding::UTF_8)
    i = s.inspect
    f.puts("#{cp}\t#{i.codepoints.join(",")}") unless i == %Q("#{s}")
  end
end
