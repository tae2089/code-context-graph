; Functions — global (name is function_name: "foo", "Foo:bar", "Foo.baz")
(function_statement
  name: (function_name) @name.function) @definition.function

; Functions — local (name is identifier: "bar")
(function_statement
  name: (identifier) @name.function) @definition.function

; Calls — simple and method calls (prefix is always identifier in this grammar)
(function_call
  prefix: (identifier) @name.call) @reference.call
