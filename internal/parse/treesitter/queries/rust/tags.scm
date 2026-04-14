; Structs
(struct_item
  name: (type_identifier) @name.class) @definition.class

; Enums
(enum_item
  name: (type_identifier) @name.class) @definition.class

; Traits
(trait_item
  name: (type_identifier) @name.interface) @definition.interface

; Impl
(impl_item
  type: (type_identifier) @name.class) @definition.class

; Impl methods
(impl_item
  type: (type_identifier) @name.receiver
  body: (declaration_list
    (function_item
      name: (identifier) @name.function) @definition.function))

; Trait Impl Methods
(impl_item
  trait: (type_identifier) @reference.implements
  type: (type_identifier) @name.class) @definition.class

; Functions
(function_item
  name: (identifier) @name.function) @definition.function

; Calls
(call_expression
  function: (_) @name.call) @reference.call

; Imports
(use_declaration
  argument: (_) @name.import) @reference.import
