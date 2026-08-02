# Trailing valid JSON

The only record line contains a complete JSON object followed by a second,
complete JSON value. Ruby rejects it as one malformed JSONL record; it must not
silently accept the first value or misclassify the second as a non-object line.
