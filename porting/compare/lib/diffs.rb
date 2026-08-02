# frozen_string_literal: true

require "json"

module Conformance
  # Difference *descriptions*. These do not decide anything — they turn "these
  # two values are not equal" into "the key `tags` is present on the baseline
  # side and absent on the candidate side, at $[3]". The decision (is it a
  # difference at all, and what class is it) lives in the dimensions.
  module Diffs
    module_function

    # --- structured stdout ---------------------------------------------------

    # Classify a captured stream's bytes as parsed data or as text.
    #
    # porting/specs/errors.md § "Structured errors are compared as data": where
    # a command emits JSON, the comparison is over the parsed value, not the
    # serialized bytes. Everything else is diagnostic text and is compared byte
    # for byte.
    #
    # Returns [:json, value] | [:jsonl, [values]] | [:text, bytes].
    def classify_stream(bytes)
      return [:text, bytes] if bytes.nil?

      text = bytes.dup.force_encoding(Encoding::UTF_8)
      return [:text, bytes] unless text.valid_encoding?

      stripped = text.strip
      return [:text, bytes] if stripped.empty?
      return [:text, bytes] unless stripped.start_with?("{", "[")

      begin
        return [:json, JSON.parse(stripped)]
      rescue JSON::ParserError
        # fall through to JSONL
      end

      lines = text.each_line.map(&:strip).reject(&:empty?)
      return [:text, bytes] if lines.empty?

      begin
        [:jsonl, lines.map { |l| JSON.parse(l) }]
      rescue JSON::ParserError
        [:text, bytes]
      end
    end

    # Deep structural diff of two parsed JSON values.
    #
    # Object key ORDER is not reported, because Ruby's Hash#== ignores insertion
    # order and errors.md says stdout JSON is consumed by parsers. Array order
    # IS reported, because errors.md says array order is significant —
    # diagnostics are emitted in file order and that order is part of what makes
    # `check` readable.
    #
    # A key present on one side and absent on the other is reported as its own
    # reason even when the value would be null: "an omitted key and a null key
    # are different answers".
    def json_diff(a, b, path = "$", out = [], limit: 25)
      return out if out.length >= limit

      # `true` and `false` are different Ruby classes and the same JSON type, so
      # they are compared by value like any other scalar. Without this, flipping
      # a boolean reports itself as a type change from "boolean" to "boolean" —
      # a real difference, described in a way that reads as a harness bug.
      if a.class != b.class && !(numeric?(a) && numeric?(b)) && !(boolean?(a) && boolean?(b))
        out << { "path" => path, "reason" => "type",
                 "baseline" => type_name(a), "candidate" => type_name(b),
                 "baseline_value" => a, "candidate_value" => b }
        return out
      end

      case a
      when Hash
        (a.keys | b.keys).sort_by(&:to_s).each do |k|
          break if out.length >= limit

          child = "#{path}.#{k}"
          if !a.key?(k)
            out << { "path" => child, "reason" => "key_only_in_candidate",
                     "baseline" => nil, "candidate" => b[k] }
          elsif !b.key?(k)
            out << { "path" => child, "reason" => "key_only_in_baseline",
                     "baseline" => a[k], "candidate" => nil }
          else
            json_diff(a[k], b[k], child, out, limit: limit)
          end
        end
      when Array
        if a.length != b.length
          out << { "path" => path, "reason" => "array_length",
                   "baseline" => a.length, "candidate" => b.length }
        end
        [a.length, b.length].min.times do |i|
          break if out.length >= limit

          json_diff(a[i], b[i], "#{path}[#{i}]", out, limit: limit)
        end
      else
        unless a == b
          out << { "path" => path, "reason" => "value", "baseline" => a, "candidate" => b }
        end
      end
      out
    end

    def numeric?(v) = v.is_a?(Numeric) && !v.is_a?(TrueClass)
    def boolean?(v) = v == true || v == false

    def type_name(v)
      case v
      when nil then "null"
      when true, false then "boolean"
      when Integer then "integer"
      when Float then "number"
      when String then "string"
      when Array then "array"
      when Hash then "object"
      else v.class.name
      end
    end

    # --- bytes ---------------------------------------------------------------

    # First differing byte offset, and a readable window around it. Used for
    # stderr and for store bytes, where the comparison is byte-for-byte and the
    # useful thing to report is *where*.
    def byte_diff(a, b)
      a = a.dup.force_encoding(Encoding::ASCII_8BIT)
      b = b.dup.force_encoding(Encoding::ASCII_8BIT)
      offset = 0
      offset += 1 while offset < a.bytesize && offset < b.bytesize && a.getbyte(offset) == b.getbyte(offset)
      {
        "first_differing_byte" => offset,
        "baseline_size" => a.bytesize,
        "candidate_size" => b.bytesize,
        "baseline_window" => window(a, offset),
        "candidate_window" => window(b, offset)
      }
    end

    def window(bytes, offset, radius: 40)
      from = [offset - radius, 0].max
      slice = bytes.byteslice(from, radius * 2).to_s
      slice.force_encoding(Encoding::UTF_8)
      slice = slice.scrub("·") unless slice.valid_encoding?
      "#{"…" if from.positive?}#{slice.inspect[1..-2]}"
    end

    # Line-level diff for JSONL stores and journal indexes, where a byte offset
    # is true but useless and the record that changed is what a reader needs.
    def line_diff(a, b, limit: 12)
      al = decode_lines(a)
      bl = decode_lines(b)
      diffs = []
      [al.length, bl.length].max.times do |i|
        break if diffs.length >= limit
        next if al[i] == bl[i]

        diffs << { "line" => i + 1, "baseline" => al[i], "candidate" => bl[i] }
      end
      { "baseline_lines" => al.length, "candidate_lines" => bl.length, "lines" => diffs }
    end

    def decode_lines(bytes)
      bytes.to_s.dup.force_encoding(Encoding::UTF_8).scrub("·").split("\n", -1).tap do |l|
        l.pop if l.last == ""
      end
    end
  end
end
