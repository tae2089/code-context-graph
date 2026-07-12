; Classes
(class_declaration
  name: (name) @name.class) @definition.class

; Interfaces
(interface_declaration
  name: (name) @name.interface) @definition.interface

; Methods
(method_declaration
  name: (name) @name.function) @definition.function

; Functions
(function_definition
  name: (name) @name.function) @definition.function

; Calls
(function_call_expression
  function: (name) @name.call) @reference.call
(scoped_call_expression
  name: (name) @name.call) @reference.call
(member_call_expression
  name: (name) @name.call) @reference.call

; Imports
(namespace_use_clause
  (qualified_name) @name.import) @reference.import
