; Classes, Structs, Extensions
(class_declaration
  name: [(type_identifier) (user_type (type_identifier))] @name.class) @definition.class

; Class/Extension Methods
(class_declaration
  name: [(type_identifier) (user_type (type_identifier))] @name.receiver
  (class_body
    (function_declaration
      name: (simple_identifier) @name.function) @definition.function))

; Interfaces
(protocol_declaration
  name: (type_identifier) @name.interface) @definition.interface

; Functions
(function_declaration
  name: (simple_identifier) @name.function) @definition.function

; Calls
(call_expression
  (_) @name.call) @reference.call

; Imports
(import_declaration
  (_) @name.import) @reference.import
