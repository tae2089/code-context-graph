; Classes
(class_definition
  name: (identifier) @name.class) @definition.class

; Functions and Methods
(function_definition
  name: (identifier) @name.function) @definition.function

; Calls
(call
  function: (_) @name.call) @reference.call

; Imports
(import_statement
  name: (dotted_name) @name.import) @reference.import
(import_statement
  name: (aliased_import (dotted_name) @name.import)) @reference.import
(import_from_statement
  module_name: (dotted_name) @name.import) @reference.import
