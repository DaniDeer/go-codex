# HATEOAS in go-codex

> **Prerequisite shipped**: `codex.Template[T]` (build+match engine for
> every `{varName}` pattern) and `codex.Param`/`MergedParam[T]`/`NewParam[T]`
> (the shared validate/merge primitive underneath `rest.PathParam`/
> `events.TopicParam`/`reqreply.TopicParam`/`ports.FilePathParam`) are now
> shipped — see `codex/template.go`/`codex/param.go` and
> `docs/concepts/codec.md`'s matching subsections. Any future
> `Affordance[T,V]`-style HATEOAS link resolution described below would
> call `Template[V].Codec().Encode`/`Build` directly to resolve a link's
> href from typed vars, exactly the mechanism this doc's design review
> anticipated.

## My Prompt to ChatGPT:

Look at the current state of my go-codex lib: 
DaniDeer/go-codex I want to support codec-defined HATEOAS for my api.rest definition and an easy, declarative workflow for the user working with the rest adapters/ports. One principle of go-codex is "one-struct-one-call" where everything (headers, cookies, query and path params and the body) can be derived from a codec-defined struct.

## ChatGPT´s response

Yes. Looking at the current `go-codex` state, I think HATEOAS is a **very natural next step**, but I would avoid treating it as merely “add a `Links` field to response structs.”

The stronger idea is:

> **A REST codec should describe not only the representation of a resource, but also the actions that can be taken on that representation.**

That fits your existing philosophy extremely well.

Your current architecture already has the right foundations: `api/rest` defines typed routes, codecs provide the data contract, and `adapters/nethttp` consumes the route contract. You have also already moved toward the “one declaration does everything” model with `NewPathParam`, where a parameter simultaneously participates in validation/spec generation **and** request construction/merging. ([GitHub][1]) The README explicitly describes the same route definition being reusable on the client side. ([GitHub][2])

## 1. I would make HATEOAS a property of the codec

Imagine:

```go
type User struct {
    ID    string
    Name  string
    Email string
}
```

Instead of:

```go
type UserResponse struct {
    User  User  `json:"user"`
    Links Links `json:"_links"`
}
```

I'd let the **codec define the representation and its affordances**:

```go
var UserCodec = rest.Resource(
    codex.Struct[User](
        codex.RequiredField("id", ...),
        codex.RequiredField("name", ...),
        codex.RequiredField("email", ...),
    ),

    rest.Link("self", GetUser),
    rest.Link("update", UpdateUser),
    rest.Link("delete", DeleteUser),
)
```

The important distinction is that `GetUser`, `UpdateUser`, and `DeleteUser` aren't arbitrary URLs.

They are **typed REST route contracts**.

That gives you a very powerful relationship:

```text
User codec
   │
   ├── representation
   │
   ├── validation
   │
   ├── schema
   │
   └── affordances
          │
          ├── GET /users/{id}
          ├── PATCH /users/{id}
          └── DELETE /users/{id}
```

And consequently:

```text
             ┌──────────────┐
             │  UserCodec   │
             └──────┬───────┘
                    │
       ┌────────────┼─────────────┐
       ▼            ▼             ▼
    JSON/YAML    OpenAPI       HATEOAS
       │            │             │
       ▼            ▼             ▼
 representation   contract      actions
```

That is much more aligned with the central go-codex idea than having a separate HATEOAS framework bolted onto REST.

---

# 2. The really interesting part: links should point to routes, not strings

I'd strongly avoid an API like:

```go
rest.Link("self", "/users/{id}")
```

because that throws away the type information you already have.

Instead:

```go
rest.Link("self", GetUser)
```

where:

```go
var GetUser = rest.NewRoute[GetUserRequest, User](
    "GET",
    "/users/{id}",
    getUserRequestCodec,
    UserCodec,
)
```

Now the link knows:

* HTTP method
* URI template
* path parameters
* query parameters
* headers
* cookies
* request body
* request codec
* response codec
* OpenAPI metadata

This is where your **one-struct-one-call** principle becomes extremely powerful.

---

# 3. I'd actually call this an "affordance", not just a "link"

HATEOAS has two levels:

### Link

```json
{
  "rel": "self",
  "href": "/users/42"
}
```

### Action / affordance

```json
{
  "rel": "update",
  "href": "/users/42",
  "method": "PATCH"
}
```

And potentially:

```json
{
  "rel": "update",
  "href": "/users/42",
  "method": "PATCH",
  "contentType": "application/json"
}
```

Your route already contains almost all of this information.

So I'd introduce something conceptually like:

```go
type Affordance[T any] struct {
    Rel   string
    Route Route[any, T]
}
```

or, more likely, a non-generic internal representation plus generic constructors.

The key idea:

> **A HATEOAS affordance should reference a `rest.Route`, not independently describe an HTTP operation.**

That prevents the route and its HATEOAS representation from drifting apart.

---

# 4. Then the client becomes really interesting

Suppose the server returns:

```json
{
  "id": "42",
  "name": "Alice",
  "_links": {
    "self": {
      "href": "/users/42"
    },
    "delete": {
      "href": "/users/42",
      "method": "DELETE"
    },
    "orders": {
      "href": "/users/42/orders"
    }
  }
}
```

Instead of making the consumer manually construct URLs:

```go
client.DeleteUser(ctx, user.ID)
```

I'd like the declarative workflow to become something like:

```go
user, err := client.Get(ctx, GetUserRequest{ID: "42"})
```

and then:

```go
err := user.Delete(ctx)
```

or perhaps:

```go
err := client.Follow(ctx, user.Links["delete"])
```

But I'd go one step further.

---

# 5. The response should carry typed affordances

Conceptually:

```go
type Resource[T any] struct {
    Value T
    Links Links
}
```

Then:

```go
user, err := client.Get(ctx, GetUser, GetUserRequest{
    ID: "42",
})
```

returns something like:

```go
Resource[User]
```

with:

```go
user.Value
user.Links
```

And:

```go
user.Links.Self()
user.Links.Delete(ctx)
user.Links.Orders(ctx)
```

could potentially be typed.

The important thing is that the server response is no longer merely:

```text
JSON → struct
```

but:

```text
HTTP response
    ↓
codec
    ↓
representation + affordances
    ↓
typed client workflow
```

That is **true HATEOAS**, rather than simply emitting `_links`.

---

# 6. But there is a subtle problem: runtime links are dynamic

This is where I would introduce a distinction between:

### Route

A compile-time API contract:

```go
GetUser
```

and:

### Link

A runtime realization:

```go
{
    Rel:  "self",
    Href: "/users/42",
    Route: GetUser,
}
```

So:

```text
Route
  │
  │ instantiate with values
  ▼
Link
  │
  │ follow
  ▼
HTTP request
```

This is important because the route contains the **URI template**, whereas the link contains the **resolved URI**.

---

# 7. This connects beautifully to your existing merged parameter work

You currently have this concept:

```go
rest.NewPathParam(
    "id",
    codex.String().Refine(validate.UUID),
    func(r GetUserReq) string { return r.ID },
    func(r *GetUserReq, v string) { r.ID = v },
)
```

The repository explicitly describes this as allowing the same declaration to participate in validation, OpenAPI, and automatic merging into the request. ([GitHub][1])

I'd extend that concept rather than create another parameter system.

For example:

```go
var GetUser = rest.NewRoute[GetUserRequest, User](
    "GET",
    "/users/{id}",

    getUserCodec,
    UserCodec,

    rest.NewPathParam(
        "id",
        codex.String(),
        func(r GetUserRequest) string { return r.ID },
        func(r *GetUserRequest, v string) { r.ID = v },
    ),
)
```

Now the route knows exactly how to turn:

```go
GetUserRequest{ID: "42"}
```

into:

```text
GET /users/42
```

HATEOAS can reuse that mechanism in reverse.

Given:

```text
/users/42
```

the client can resolve:

```text
id = "42"
```

into the request struct.

That's extremely powerful.

---

# 8. This leads to what I think is the killer feature

You want:

> an easy, declarative workflow for the user working with the REST adapters/ports.

I would make the **port declaration itself declarative**.

Something like:

```go
type UserPort struct {
    Get    rest.Port[GetUserRequest, User]
    Create rest.Port[CreateUserRequest, User]
    Update rest.Port[UpdateUserRequest, User]
    Delete rest.Port[DeleteUserRequest, Empty]
}
```

Or even:

```go
var Users = rest.ResourcePort(
    rest.GET(GetUser),
    rest.POST(CreateUser),
    rest.PATCH(UpdateUser),
    rest.DELETE(DeleteUser),
)
```

Then an adapter is simply:

```go
users := nethttp.NewPort(httpClient, Users)
```

and application code becomes:

```go
user, err := users.Get(ctx, GetUserRequest{
    ID: id,
})
```

No HTTP details.

No URL construction.

No query encoding.

No header construction.

No cookie handling.

No JSON marshaling.

Those all remain properties of the codec/route.

---

# 9. And HATEOAS becomes the dynamic version of the same port

This is the architectural model I'd aim for:

```text
                    REST contract
                         │
             ┌───────────┴───────────┐
             │                       │
          Routes                 Resources
             │                       │
             │                 HATEOAS links
             │                       │
             └───────────┬───────────┘
                         │
                    REST Port
                         │
              ┌──────────┴──────────┐
              │                     │
         Server Adapter        Client Adapter
              │                     │
          net/http             net/http
```

So there isn't really a separate "HATEOAS subsystem".

It's another projection of the **same REST contract**.

That's very go-codex.

---

# 10. I'd also make links codec-defined at the representation level

For example:

```go
var UserCodec = rest.ResourceCodec(
    codex.Struct[User](
        ...
    ),

    rest.Affordances(
        rest.Self(GetUser),
        rest.Rel("orders", ListUserOrders),
        rest.Rel("update", UpdateUser),
        rest.Rel("delete", DeleteUser),
    ),
)
```

The codec can then derive:

### JSON

```json
{
  "id": "42",
  "name": "Alice",
  "_links": {
    "self": {
      "href": "/users/42"
    },
    "orders": {
      "href": "/users/42/orders"
    },
    "update": {
      "href": "/users/42",
      "method": "PATCH"
    }
  }
}
```

### OpenAPI

The link information could contribute to operation/link metadata.

### Client

The same definition enables:

```go
user.Links.Update(...)
```

### Documentation

The codec can explain the resource's available state transitions.

One declaration.

Multiple projections.

Again exactly the core go-codex principle. The repository currently describes the library as deriving JSON/YAML/OpenAPI/AsyncAPI from one codec, so HATEOAS fits this "derive another representation" philosophy particularly well. ([GitHub][2])

---

# 11. I would separate `Link` and `Action`

I'd probably model the concepts roughly as:

```go
type Link struct {
    Rel    string
    Href   string
    Method string
}
```

but internally:

```go
type Affordance struct {
    Rel   string
    Route RouteRef
}
```

Then distinguish:

```go
rest.Link("self", GetUser)
```

from:

```go
rest.Action("delete", DeleteUser)
```

because a GET link and a DELETE affordance have different semantics.

Potentially:

```go
rest.Self(GetUser)
rest.Rel("orders", ListOrders)
rest.Action("update", UpdateUser)
rest.Action("delete", DeleteUser)
```

That gives you a very pleasant DSL.

---

# 12. Don't put HATEOAS directly into every domain struct

This is the one design decision I'd be particularly careful about.

I would **not** encourage:

```go
type User struct {
    ID    string
    Name  string
    Links map[string]Link
}
```

because now your domain model becomes HTTP-aware.

Instead:

```go
type User struct {
    ID   string
    Name string
}
```

and:

```go
type UserResource struct {
    Value User
    Links Links
}
```

or perhaps have the codec produce a representation wrapper.

That preserves your hexagonal architecture nicely:

```text
domain
   │
   │ User
   ▼
REST representation
   │
   ├── User
   └── affordances
```

The domain doesn't know HATEOAS exists.

---

# 13. The "one-struct-one-call" principle could become even stronger

I'd phrase the principle slightly more broadly:

> **One struct describes one interaction.**

For a request:

```go
type UpdateUserRequest struct {
    ID    string
    Name  string
    Email string
}
```

the codec/route derives:

```text
path     → ID
body     → Name, Email
headers  → codec-defined fields
query    → codec-defined fields
cookies  → codec-defined fields
```

For a HATEOAS link:

```text
runtime link
     ↓
route
     ↓
request struct
     ↓
codec
     ↓
HTTP request
```

So a link doesn't need to expose a pile of HTTP primitives to the application.

The application works with the **same request struct it would have used with the statically declared route**.

That is a really elegant model.

---

# 14. Example of the workflow I'd aim for

Imagine an API:

```text
GET    /orders/{id}
PATCH  /orders/{id}
DELETE /orders/{id}
POST   /orders/{id}/cancel
GET    /orders/{id}/items
```

The user declares:

```go
var Order = rest.Resource(
    OrderCodec,

    rest.Self(GetOrder),
    rest.Action("update", UpdateOrder),
    rest.Action("delete", DeleteOrder),
    rest.Action("cancel", CancelOrder),
    rest.Rel("items", ListOrderItems),
)
```

The server automatically produces:

```json
{
  "id": "123",
  "status": "pending",
  "_links": {
    "self": {
      "href": "/orders/123"
    },
    "update": {
      "href": "/orders/123",
      "method": "PATCH"
    },
    "delete": {
      "href": "/orders/123",
      "method": "DELETE"
    },
    "cancel": {
      "href": "/orders/123/cancel",
      "method": "POST"
    },
    "items": {
      "href": "/orders/123/items"
    }
  }
}
```

Then a client can do conceptually:

```go
order, err := orders.Get(ctx, GetOrderRequest{
    ID: "123",
})

if order.Can("cancel") {
    order, err = order.Cancel(ctx)
}
```

The **really nice part** is that `Cancel` doesn't need to know how `/orders/123/cancel` is constructed. The link already contains the resolved route information.

---

# 15. I would make the adapter generic over the transport

Your existing `nethttp` adapter is already positioned as the concrete HTTP implementation; the directory currently contains separate adapter, binding, client and cookie concerns. ([GitHub][3])

I'd therefore keep the REST port transport-independent:

```go
type Client interface {
    Do(...)
}
```

or preferably a very small internal transport abstraction.

Then:

```text
api/rest
    ↓
REST Port / Route / Resource
    ↓
adapters/nethttp
```

The port should never know about `http.Client`.

The adapter should know about:

* `http.Request`
* `http.Response`
* cookies
* connection
* TLS
* retries
* transport

while `api/rest` knows about:

* method
* URI template
* request codec
* response codec
* parameters
* affordances
* contract

This keeps the architecture clean.

---

# 16. One additional idea: `Follow`

I'd expose a generic primitive underneath the typed conveniences:

```go
result, err := client.Follow(ctx, link)
```

This gives you true hypermedia navigation.

Then higher-level APIs can be syntactic sugar:

```go
order.Links.Cancel(ctx)
```

which internally is:

```go
client.Follow(ctx, order.Links.Action("cancel"))
```

That gives you two modes:

### Typed/static

```go
orders.Get(ctx, GetOrderRequest{ID: id})
```

### Hypermedia/dynamic

```go
client.Follow(ctx, order.Link("items"))
```

And both ultimately use the **same route/codec machinery**.

---

# 17. The architecture I'd pursue

I think the cleanest evolution of go-codex is:

```text
                       codex.Codec[T]
                             │
                             ▼
                    ┌─────────────────┐
                    │ REST Resource    │
                    │                 │
                    │ representation  │
                    │ affordances     │
                    └────────┬────────┘
                             │
                 ┌───────────┴───────────┐
                 ▼                       ▼
              Route                  HATEOAS
                 │                       │
          request/response          runtime Link
              codecs                    │
                 │                       │
                 └───────────┬───────────┘
                             ▼
                         REST Port
                             │
                   ┌─────────┴─────────┐
                   ▼                   ▼
              Server Adapter      Client Adapter
                   │                   │
               net/http            net/http
```

And the central invariant becomes:

> **There must be exactly one source of truth for an HTTP interaction: the route + codecs.**

No duplicated URL templates.

No duplicated request structs.

No duplicated parameter definitions.

No manually maintained HATEOAS URLs.

No separately maintained OpenAPI operation.

That would make HATEOAS a **first-class consequence of your existing design**, rather than an additional feature.

---

## My recommendation for the API shape

I'd explore these four primitives first:

```go
rest.Resource(...)
rest.Self(...)
rest.Link(...)
rest.Action(...)
```

with:

```go
rest.ResourceCodec[T](codec, affordances...)
```

and internally:

```go
Route
RouteRef
Affordance
Link
```

Then build the adapter API around:

```go
port.Call(ctx, route, request)
port.Follow(ctx, link)
```

with typed wrappers layered on top.

That gives you a very small conceptual core while allowing an extremely ergonomic API.

And importantly, **I would not start by designing the JSON `_links` representation**. Start with the internal relationship:

```text
Codec → Resource → Affordance → Route → Request struct → Adapter
```

Once that is right, HAL, JSON:API-style links, Siren, plain `_links`, or your own representation can all become renderers. Your current architecture is explicitly built around deriving multiple external representations from one contract, so this keeps HATEOAS consistent with the rest of go-codex. ([GitHub][2])

If you want to push this further, I think the **most interesting next design question is how `rest.Resource[T]` should interact with `Route[Req, Resp]` and Go generics**—because getting that relationship right could make the entire client-side REST API almost disappear behind typed ports. ([GitHub][1])

[1]: https://github.com/DaniDeer/go-codex/blob/main/api/rest/builder.go "go-codex/api/rest/builder.go at main · DaniDeer/go-codex · GitHub"
[2]: https://github.com/DaniDeer/go-codex "GitHub - DaniDeer/go-codex: A self-documenting codec library for Go — declare shape, constraints, and schema once; derive JSON, YAML, OpenAPI, AsyncAPI and more for free. · GitHub"
[3]: https://github.com/DaniDeer/go-codex/tree/main/adapters/nethttp "go-codex/adapters/nethttp at main · DaniDeer/go-codex · GitHub"
