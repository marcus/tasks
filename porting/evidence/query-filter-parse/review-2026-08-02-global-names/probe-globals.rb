# Characterise the two global-name rules for the review.
dash = ["a", "Z", "0", "9", "_", "!", " ", ".", "-", "$", "", "",
        "", " ", "​", "é", "あ", "\u{1F600}",
        "�", "ab", ""]
dash.each do |suffix|
  name = "$-#{suffix}"
  puts format("%-24s => %s", name.dump, name.to_sym.inspect.dump)
end
puts "---"
%w[$0 $1 $9 $10 $19 $190 $00 $01 $09 $90 $1a $0a].each do |name|
  puts format("%-8s => %s", name, name.to_sym.inspect)
end
