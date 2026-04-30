Public API:

- [ ] How to use this codec in a GO CLI scenario? Discuss this and add an example to the README since GO is a favourite language for CLI tools. The obvious use case are config files that CLI tools always read (YAML/TOML). But is codec also applicable for parsing the command line inputs with commands, flags, arguments?

Error handling:

- [ ] The error model is too simple
      Right now:
  ```GO
  (T, error)
  ```
  Example:
  ```GO
  {
    "width": -1,
    "height": -2
  }
  ```
  You only get:
  ```TEXT
  width invalid
  ```
  But user want:
  ```TEXT
  width invalid
  height invalid
  ```
  Introduce:
  ```GO
  type ValidationError struct {
      Path string
      Msg  string
  }
  ```
  So that you can return multiple errors:
  ```GO
  type Result[T any] struct {
      Value  T
      Errors []ValidationError
  }
  ```

Schema:

- [ ] Schema is underpowered
      We have:
  ```GO
  Type, Properties, Required, OneOf
  ```
  But we also need:
  - additionalProperties: false
  - minimum, maximum
  - pattern
  - nullable
  - discriminator (OpenAPI)

Constraints:

Questions:

- Question: Can my codec also support HTML? Is it usefull to have something similar for HTML maybe together with templ in GO?
  Answer: Don´t force HTML into a codec - use templ directly for rendering, but a mapper/transformer between your data models and templ components is genuinely useful. Use cases:
  - Mapping API responses -> templ props
  - Sanitizing/escaping HTML content
  - Converting markdown/rich text -> HTML
    But not: Full HTML encode/decode
