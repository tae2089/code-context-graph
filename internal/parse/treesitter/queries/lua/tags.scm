; Functions
(function_statement
  name: [
    (function_name) 
    (identifier)
    (method_index_expression)
  ] @name.function) @definition.function

(local_function_statement
  name: (identifier) @name.function) @definition.function

; Calls
(function_call
  prefix: [
    (identifier)
    (method_index_expression)
  ] @name.call) @reference.call
