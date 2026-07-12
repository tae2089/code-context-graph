; Classes
(class_definition
  name: (identifier) @name.class) @definition.class

; Functions and Methods
(function_definition
  name: (identifier) @name.function) @definition.function

; Decorated definitions: 데코레이터가 있는 함수/클래스는 decorated_definition 래퍼 노드가
; 첫 데코레이터 줄부터 시작하므로, 이를 별도 매칭하여 StartLine을 데코레이터 첫 줄로 잡는다.
; 기존 function_definition/class_definition 매칭과 중복되지만, walker의 nameIndex 기반
; 중복 제거 로직이 StartLine이 더 작은(데코레이터 첫 줄) 쪽을 우선 보존한다.
(decorated_definition
  definition: (function_definition
    name: (identifier) @name.function)) @definition.function

(decorated_definition
  definition: (class_definition
    name: (identifier) @name.class)) @definition.class

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
