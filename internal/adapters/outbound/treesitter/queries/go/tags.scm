; Package
(package_clause
  (package_identifier) @name.package) @definition.package

; Interfaces
(type_declaration
  (type_spec
    name: (type_identifier) @name.interface
    type: (interface_type))) @definition.interface

; Structs
(type_declaration
  (type_spec
    name: (type_identifier) @name.class
    type: (struct_type))) @definition.class

; Type (fallback if not interface or struct)
(type_declaration
  (type_spec
    name: (type_identifier) @name.type)) @definition.type

; Methods
(method_declaration
  receiver: (parameter_list (parameter_declaration type: (_) @name.receiver))
  name: (field_identifier) @name.function) @definition.method

; Functions
(function_declaration
  name: (identifier) @name.function) @definition.function

; Calls
(call_expression
  function: (_) @name.call) @reference.call

; Imports
(import_spec
  path: [(interpreted_string_literal) (raw_string_literal)] @name.import) @reference.import
