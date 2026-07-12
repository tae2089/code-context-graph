; Classes
(class_declaration
  name: (type_identifier) @name.class) @definition.class

; Decorated exported classes: tree-sitter-typescript places decorators as siblings
; of class_declaration inside export_statement, so class_declaration.StartLine
; stays at the class keyword line. Match the export_statement wrapper so its
; StartLine (first decorator line) wins via nameIndex dedup.
(export_statement
  (class_declaration
    name: (type_identifier) @name.class)) @definition.class

; Interfaces
(interface_declaration
  name: (type_identifier) @name.interface) @definition.interface

; Types
(type_alias_declaration
  name: (type_identifier) @name.type) @definition.type

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
