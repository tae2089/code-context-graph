; Classes
(class_definition
  name: (identifier) @name.class) @definition.class

(object_definition
  name: (identifier) @name.class) @definition.class

; Traits
(trait_definition
  name: (identifier) @name.interface) @definition.interface

; Functions
(function_definition
  name: (identifier) @name.function) @definition.function

; Calls
(call_expression
  function: (_) @name.call) @reference.call

; Imports
(import_declaration
  path: (identifier) @name.import) @reference.import
