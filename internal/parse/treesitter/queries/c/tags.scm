; Structs
(struct_specifier
  name: [(type_identifier) (identifier)] @name.class) @definition.class

; Functions
(function_definition
  declarator: (function_declarator
    declarator: (identifier) @name.function)) @definition.function

(declaration
  declarator: (function_declarator
    declarator: (identifier) @name.function)) @definition.function

; Calls
(call_expression
  function: (_) @name.call) @reference.call

; Includes
(preproc_include
  path: [(string_literal) (system_lib_string)] @name.import) @reference.import
