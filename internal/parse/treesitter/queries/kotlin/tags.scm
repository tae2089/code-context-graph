; Classes
(class_declaration
  (type_identifier) @name.class) @definition.class

; Object declarations
(object_declaration
  (type_identifier) @name.class) @definition.class

; Functions
(function_declaration
  (simple_identifier) @name.function) @definition.function

; Calls
(call_expression
  (simple_identifier) @name.call) @reference.call

(call_expression
  (navigation_expression
    (navigation_suffix
      (simple_identifier) @name.call))) @reference.call

; Imports
(import_header
  (identifier) @name.import) @reference.import
