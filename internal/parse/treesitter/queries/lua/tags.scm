; Functions
(function_statement
  name: [(function_name) (identifier)] @name.function) @definition.function

; Calls
(function_call
  prefix: (identifier) @name.call) @reference.call
