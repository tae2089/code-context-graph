; Classes
(class_declaration
  name: (identifier) @name.class) @definition.class

; Interfaces
(interface_declaration
  name: (identifier) @name.interface) @definition.interface

; Methods
(method_declaration
  name: (identifier) @name.function) @definition.function

; Calls
(invocation_expression
  function: (_) @name.call) @reference.call

; Namespaces
(namespace_declaration
  name: (identifier) @name.package) @definition.package
(file_scoped_namespace_declaration
  name: (identifier) @name.package) @definition.package

; Imports
(using_directive
  (_) @name.import) @reference.import
