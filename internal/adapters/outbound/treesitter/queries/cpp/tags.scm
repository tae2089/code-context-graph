; Classes
(class_specifier
  name: [(type_identifier) (identifier)] @name.class) @definition.class

(struct_specifier
  name: [(type_identifier) (identifier)] @name.class) @definition.class

; Namespaces
(namespace_definition
  name: (namespace_identifier) @name.package) @definition.package

; Functions
(function_definition
  declarator: (function_declarator
    declarator: [(identifier) (field_identifier)] @name.function)) @definition.function

; Calls
(call_expression
  function: (_) @name.call) @reference.call

; Includes
(preproc_include
  path: [(string_literal) (system_lib_string)] @name.import) @reference.import
