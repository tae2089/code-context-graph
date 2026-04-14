; Functions
(function_definition
  name: (word) @name.function) @definition.function

; Calls
(command
  name: (command_name) @name.call) @reference.call

; Imports
(command
  name: (command_name (word) @cmd_name
    (#match? @cmd_name "^(source|\\.)$"))
  argument: (word) @name.import) @reference.import
