// delegation-probe mirrors porting/runners/ruby/delegation-probe for direct conformance.
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"tasks-go/internal/record"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: delegation-probe CASES.jsonl")
		os.Exit(2)
	}
	input, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, line := range bytes.Split(input, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 || bytes.HasPrefix(bytes.TrimSpace(line), []byte("#")) {
			continue
		}
		var spec map[string]json.RawMessage
		if err := json.Unmarshal(line, &spec); err != nil {
			panic(err)
		}
		var caseID string
		_ = json.Unmarshal(spec["case_id"], &caseID)
		out := map[string]any{"case_id": caseID}
		if stamp, ok := spec["stamp"]; ok {
			var values struct {
				Epoch  float64 `json:"epoch"`
				Offset string  `json:"utc_offset"`
			}
			_ = json.Unmarshal(stamp, &values)
			if values.Offset == "" {
				values.Offset = "+00:00"
			}
			seconds := int(values.Epoch)
			nanoseconds := int((values.Epoch - float64(seconds)) * 1e9)
			parsed := time.Unix(int64(seconds), int64(nanoseconds))
			parsedOffset, _ := time.Parse("-07:00", values.Offset)
			_, offsetSeconds := parsedOffset.Zone()
			zone := time.FixedZone(values.Offset, offsetSeconds)
			out["kind"] = "stamp"
			out["input"] = map[string]any{"epoch": values.Epoch, "utc_offset": values.Offset, "iso8601": parsed.In(zone).Format("2006-01-02T15:04:05-07:00")}
			out["stamp"] = record.DelegationStamp(parsed)
			out["round_trips"] = record.DelegationTimestamp(out["stamp"])
		} else {
			value, order := materialize(spec["value"])
			out["kind"] = "shape"
			out["errors"] = record.DelegationErrors(value)
			out["valid"] = record.DelegationValid(value)
			out["unknown_keys"] = record.DelegationUnknownKeys(value)
			ordered := value
			var orderedKeys any
			if object, ok := value.(map[string]any); ok {
				keys := record.DelegationOrderedKeys(object, order)
				copy := map[string]any{}
				for _, key := range keys {
					copy[key] = transport(object[key])
				}
				ordered = copy
				orderedKeys = keys
			}
			out["ordered"] = transport(ordered)
			out["ordered_keys"] = orderedKeys
			out["predicates"] = map[string]any{"object": record.DelegationObject(value), "agent": record.DelegationAgent(value), "human": record.DelegationHuman(value), "ready": record.DelegationReady(value), "claimed": record.DelegationClaimed(value), "timestamp_at": record.DelegationTimestamp(at(value))}
			out["value"] = transport(value)
		}
		if notes, ok := spec["notes"]; ok {
			var value string
			_ = json.Unmarshal(notes, &value)
			out["notes"] = value
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			panic(err)
		}
		fmt.Println(string(encoded))
	}
}

func materialize(raw json.RawMessage) (any, []string) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(&value)
	return materializeValue(value), objectOrder(raw)
}
func materializeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if hexText, ok := typed["$bytes_hex"].(string); ok && len(typed) == 1 {
			decoded, _ := hex.DecodeString(hexText)
			return string(decoded)
		}
		for key, child := range typed {
			typed[key] = materializeValue(child)
		}
		return typed
	case []any:
		for i := range typed {
			typed[i] = materializeValue(typed[i])
		}
		return typed
	default:
		return value
	}
}
func objectOrder(raw json.RawMessage) []string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil
	}
	keys := []string{}
	for decoder.More() {
		token, _ := decoder.Token()
		keys = append(keys, token.(string))
		var discard json.RawMessage
		_ = decoder.Decode(&discard)
	}
	return keys
}
func at(value any) any {
	if object, ok := value.(map[string]any); ok {
		return object["at"]
	}
	return nil
}
func transport(value any) any {
	if text, ok := value.(string); ok && !utf8Valid(text) {
		return map[string]string{"$tasks_delegation_probe_type": "invalid-utf8-string", "bytes_hex": hex.EncodeToString([]byte(text))}
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			typed[key] = transport(child)
		}
	case []any:
		for i := range typed {
			typed[i] = transport(typed[i])
		}
	}
	return value
}
func utf8Valid(value string) bool { return bytes.Equal([]byte(value), []byte(string([]rune(value)))) }
