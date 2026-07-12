; Classes
(class
  name: [(constant) (scope_resolution)] @name.class) @definition.class

; Modules
(module
  name: [(constant) (scope_resolution)] @name.class) @definition.class

; Methods
(method
  name: [(identifier) (constant)] @name.function) @definition.function

(singleton_method
  name: [(identifier) (constant)] @name.function) @definition.function

; Calls
(call
  method: (identifier) @name.call) @reference.call
