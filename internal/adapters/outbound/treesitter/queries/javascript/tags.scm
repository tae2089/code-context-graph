; Classes
(class_declaration
  name: (identifier) @name.class) @definition.class

; Functions
(function_declaration
  name: (identifier) @name.function) @definition.function

(method_definition
  name: (property_identifier) @name.function) @definition.function

; Arrow functions assigned to variables
(variable_declarator
  name: (identifier) @name.function
  value: [(arrow_function) (function_expression)]) @definition.function

; Calls
(call_expression
  function: (_) @name.call) @reference.call

; Imports
(import_statement
  source: (string) @name.import) @reference.import
