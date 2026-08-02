# frozen_string_literal: true

require "json"

module Conformance
  # Formatting, and only formatting. The typed report is produced first and is
  # complete on its own; this turns it into something a human reads. Nothing in
  # here may change a verdict.
  module Report
    module_function

    def json(report) = "#{JSON.pretty_generate(report)}\n"

    def text(report)
      out = +""
      s = report["summary"]
      out << "conformance comparison\n"
      out << "  baseline:  #{report.dig("baseline", "source")} " \
             "(#{describe_impl(report["baseline"])}, #{s["cases"]} paired cases)\n"
      out << "  candidate: #{report.dig("candidate", "source")} " \
             "(#{describe_impl(report["candidate"])})\n\n"

      report["exclusions"].each do |ex|
        out << "  EXCLUDED  #{ex["case_id"]}  #{ex["field"]}\n            #{wrap(ex["reason"], 12)}\n"
      end
      out << "\n" unless report["exclusions"].empty?

      report["cases"].each do |c|
        next if c["status"] == "match"

        out << "#{c["status"].upcase}  #{c["case_id"]}\n"
        c["findings"].each { |f| out << finding_text(f) }
        out << "\n"
      end

      out << "summary\n"
      out << "  cases       #{s["cases"]}  (#{s["matched"]} match, #{s["mismatched"]} mismatch, " \
             "#{s["unpaired"]} unpaired)\n"
      out << "  findings    #{s["findings"]}  (#{s["gate_findings"]} gate-failing)\n"
      s["by_class"].sort.each { |k, v| out << "    #{k.ljust(24)} #{v}\n" }
      unless s["gate_findings_by_dimension"].empty?
        out << "  by dimension\n"
        s["gate_findings_by_dimension"].sort.each { |k, v| out << "    #{k.ljust(24)} #{v}\n" }
      end
      if s["requires_rerun"]
        out << "  NOTE  the two sides disagree in `environment`. errors.md requires a re-run with the\n" \
               "        environments matched before any other difference is classified.\n"
      end
      if s["rollback_unlabelled_cases"].to_i.positive?
        out << "  NOTE  files.rolled_back is null on both sides in #{s["rollback_unlabelled_cases"]} " \
               "of #{s["cases"]} case(s):\n" \
               "        those invocations made no machine-readable rollback report, so in them a\n" \
               "        wrote-and-reverted would be detectable only as a stderr byte difference.\n" \
               "        See porting/compare/README.md § The rollback gap.\n"
      end
      out << "\n#{report.dig("gate", "passed") ? "GATE PASS" : "GATE FAIL"}\n"
      out
    end

    def finding_text(f)
      out = +"  [#{f["class"]}/#{f["severity"]}] #{f["dimension"]}: #{f["field"]}\n"
      out << "      baseline:  #{inline(f["baseline"])}\n"
      out << "      candidate: #{inline(f["candidate"])}\n"
      out << "      rule: #{wrap(f["rule"], 12)}\n"
      out << "      re-run required: environment differs\n" if f["requires_rerun"]
      if f["detail"].is_a?(Hash) && f["detail"]["differences"]
        f["detail"]["differences"].first(6).each do |d|
          out << "      · #{d["path"]}  #{d["reason"]}  " \
                 "#{inline(d["baseline"])} -> #{inline(d["candidate"])}\n"
        end
      elsif f["detail"].is_a?(Hash) && f["detail"]["line_diff"]
        f["detail"]["line_diff"]["lines"].first(4).each do |d|
          out << "      · line #{d["line"]}\n        - #{inline(d["baseline"])}\n        + #{inline(d["candidate"])}\n"
        end
      elsif f["detail"].is_a?(Hash) && f["detail"]["first_differing_byte"]
        d = f["detail"]
        out << "      · first differing byte #{d["first_differing_byte"]} " \
               "(#{d["baseline_size"]} vs #{d["candidate_size"]} bytes)\n"
        out << "        - #{d["baseline_window"]}\n        + #{d["candidate_window"]}\n"
      elsif f["detail"].is_a?(String)
        out << "      · #{f["detail"]}\n"
      end
      out
    end

    def inline(v, limit: 120)
      s = v.is_a?(String) ? v : JSON.generate(v)
      s = s.gsub("\n", "\\n")
      s.length > limit ? "#{s[0, limit]}…" : s
    end

    def wrap(text, indent, width: 96)
      words = text.to_s.split(/\s+/)
      lines = [+""]
      words.each do |w|
        if lines.last.length + w.length + 1 > width
          lines << +""
        end
        lines.last << (lines.last.empty? ? w : " #{w}")
      end
      lines.join("\n#{" " * indent}")
    end

    def describe_impl(side)
      impls = Array(side["implementations"])
      return "unknown implementation" if impls.empty?

      impls.map { |i| "#{i["name"]} #{i["version"]}" }.uniq.join(", ")
    end
  end
end
