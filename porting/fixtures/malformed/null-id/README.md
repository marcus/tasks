# malformed/null-id

A task with an explicit JSON `null` ID. Ruby decodes this as `nil`, so `check`
reports `record missing id`; it must not classify the literal JSON spelling as
a malformed ID.
