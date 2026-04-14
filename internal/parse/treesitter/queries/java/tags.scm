; Classes
(class_declaration
  name: (identifier) @name.class) @definition.class

; Interfaces
(interface_declaration
  name: (identifier) @name.interface) @definition.interface

; Methods
(method_declaration
  name: (identifier) @name.function) @definition.function

; Annotations (Tests)
(method_declaration
  (modifiers
    (marker_annotation
      name: (identifier) @annotation_name
      (#eq? @annotation_name "Test")))
  name: (identifier) @name.test) @definition.test

; Calls
(method_invocation
  name: (identifier) @name.call) @reference.call

; Imports
(import_declaration
  (scoped_identifier) @name.import) @reference.import
