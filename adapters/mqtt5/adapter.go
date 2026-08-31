package mqtt5

import (
	"context"
	"errors"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// MQTTClient wraps the paho.golang client methods used by this adapter.
// [*paho.Client] from github.com/eclipse/paho.golang satisfies this interface.
type MQTTClient interface {
	Publish(ctx context.Context, p *pahomqtt5.Publish) (*pahomqtt5.PublishResponse, error)
	Subscribe(ctx context.Context, s *pahomqtt5.Subscribe) (*pahomqtt5.Suback, error)
	Unsubscribe(ctx context.Context, u *pahomqtt5.Unsubscribe) (*pahomqtt5.Unsuback, error)
}

// MQTTRouter routes incoming MQTT 5 messages to registered handlers.
// [*paho.StandardRouter] from github.com/eclipse/paho.golang satisfies this interface.
type MQTTRouter interface {
	RegisterHandler(topic string, handler pahomqtt5.MessageHandler)
	UnregisterHandler(topic string)
}

// UserProperty is an alias for [paho.UserProperty] for use without importing
// the underlying library directly.
type UserProperty = pahomqtt5.UserProperty

// contextKey is the unexported type for values stored in context by this package.
type contextKey struct{}

// userPropsKey stores MQTT 5 User Properties in context.
type userPropsKey struct{}

// MessageFromContext retrieves the [*paho.Publish] stored in ctx by [Subscribe].
// Returns false if ctx was not created by this package.
func MessageFromContext(ctx context.Context) (*pahomqtt5.Publish, bool) {
	msg, ok := ctx.Value(contextKey{}).(*pahomqtt5.Publish)
	return msg, ok
}

// UserPropertiesFromContext retrieves MQTT 5 User Properties from ctx.
// Returns nil, false if no User Properties were set on the incoming message.
func UserPropertiesFromContext(ctx context.Context) (pahomqtt5.UserProperties, bool) {
	props, ok := ctx.Value(userPropsKey{}).(pahomqtt5.UserProperties)
	return props, ok && len(props) > 0
}

// UserPropertyParam describes one MQTT 5 User Property to validate on incoming
// messages. It mirrors [rest.HeaderParam] for HTTP request headers.
//
// Register one or more UserPropertyParams in [SubscribeOptions.UserPropertyParams]
// or [ServeOptions.UserPropertyParams]. The adapter validates each property before
// the payload is decoded and before [SubscribeOptions.SecurityFunc] is called.
//
// Example — require a valid bearer token in the Authorization property:
//
//	opts.UserPropertyParams = []mqtt5.UserPropertyParam{
//	    {Name: "Authorization", Required: true}.WithCodec(
//	        codex.String().Refine(validate.BearerToken),
//	    ),
//	}
type UserPropertyParam struct {
	// Name is the User Property key to validate (case-sensitive).
	Name string

	// Description is emitted in documentation / guides.
	Description string

	// Required, when true, causes the adapter to reject messages that do not
	// carry this property. Rejection delivers [SubscribeError]{Kind: KindSecurity}
	// wrapping [MissingUserPropertyError].
	Required bool

	// Codec, when non-nil, validates the property value string. Validation
	// failure delivers [SubscribeError]{Kind: KindSecurity} wrapping [UserPropertyError].
	// When nil, only presence (Required) is checked.
	Codec *codex.Codec[string]
}

// WithCodec sets the validation codec and returns the updated UserPropertyParam.
// Use this to validate property values without the `&` address-of boilerplate:
//
//	{Name: "TenantID", Required: true}.WithCodec(codex.String().Refine(validate.NonEmptyString))
func (p UserPropertyParam) WithCodec(c codex.Codec[string]) UserPropertyParam {
	p.Codec = &c
	return p
}

// SubscribeOptions configures [Subscribe].
type SubscribeOptions struct {
	// TopicFilter is the MQTT subscription filter for [pahomqtt5.Subscribe].
	// Use this when the handle's topic template uses {varName} placeholders
	// (e.g. "sensors/{sensorID}/data") but the broker requires MQTT wildcard
	// syntax (e.g. "sensors/+/data"). When empty, handle.Topic is used.
	//
	// Only used by [SubscribeStream] — direct [Subscribe] callers provide
	// the filter in the pahomqtt5.Subscribe struct themselves.
	TopicFilter string

	// OnError, when non-nil, is called with a typed [SubscribeError] on decode,
	// handler, or security failure. If nil, errors are silently discarded.
	OnError func(SubscribeError)

	// Observer receives per-message lifecycle events:
	// [stats.Observer.RecordSubscribe] is called on success and failure.
	// Per-field payload validation errors are reported with location "payload".
	// Topic variable errors are reported with location "topic_var".
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer

	// SecurityFunc, when non-nil, is called for channels with non-empty security
	// requirements before fn is invoked. Return a non-nil error to reject the message.
	// MQTT 5 User Properties are available via msg.Properties.User — use them to
	// extract per-message credentials for authentication:
	//
	//	opts.SecurityFunc = func(ctx context.Context, msg *paho.Publish, reqs []route.SecurityRequirement) error {
	//	    for _, p := range msg.Properties.User {
	//	        if p.Key == "Authorization" {
	//	            return verifyJWT(p.Value, reqs)
	//	        }
	//	    }
	//	    return errors.New("missing Authorization User Property")
	//	}
	SecurityFunc func(ctx context.Context, msg *pahomqtt5.Publish, reqs []route.SecurityRequirement) error

	// UserPropertyParams, when non-nil, are validated against the incoming
	// message's MQTT 5 User Properties before [SecurityFunc] is called.
	// Validation runs per-property: missing required properties return
	// [SubscribeError]{Kind: KindSecurity} wrapping [MissingUserPropertyError];
	// codec failures return [SubscribeError]{Kind: KindSecurity} wrapping
	// [UserPropertyError].
	// Per-property validation errors are also reported via
	// [stats.Observer.RecordValidationError] with location "user_property".
	UserPropertyParams []UserPropertyParam
}

// PublishOptions configures [Publish].
type PublishOptions struct {
	// Observer receives per-publish lifecycle events.
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer

	// ContentType, when non-empty, is set as the MQTT 5 ContentType property
	// on outgoing messages. Subscribers with ContentType-aware format selection
	// will auto-select the matching format.
	ContentType string

	// UserProperties, when non-nil, are attached to outgoing MQTT 5 messages.
	// Use this to send per-message metadata (e.g. trace IDs, tenant IDs).
	UserProperties []UserProperty

	// CredentialFunc, when non-nil, is called for channels that declare
	// non-nil Publish.Security (or inherit non-empty GlobalSecurity),
	// mirroring [nethttp.CallOptions.CredentialFunc]. It must return the
	// MQTT 5 User Properties to attach as the outgoing credential — these
	// are appended to [PublishOptions.UserProperties] before publishing.
	// A nil CredentialFunc on a secured channel is not an error — the
	// message is published without a credential, same as if the channel
	// declared no security at all.
	//
	//	opts.CredentialFunc = func(ctx context.Context, reqs []route.SecurityRequirement) ([]mqtt5.UserProperty, error) {
	//	    return []mqtt5.UserProperty{{Key: "Authorization", Value: "Bearer " + token}}, nil
	//	}
	CredentialFunc func(ctx context.Context, reqs []route.SecurityRequirement) ([]UserProperty, error)
}

// Subscribe subscribes to handle.Topic and dispatches messages to fn.
//
// For each incoming message, Subscribe:
//  1. Stores the [*paho.Publish] in ctx via [MessageFromContext].
//  2. Stores MQTT 5 User Properties in ctx via [UserPropertiesFromContext].
//  3. Auto-selects the decode format: if the message carries a ContentType
//     property, the first format in formats whose [format.Format.ContentType]
//     matches is used. Otherwise the priority chain applies:
//     call-time formats > handle.SubscribeFormats > handle.Formats > handle.Decode.
//
// Subscribe calls client.Subscribe once to register the subscription with the
// broker, then registers a message handler with router. Call Subscribe once
// per channel per connection. Cancelling ctx does NOT unsubscribe from the
// broker — call client.Unsubscribe explicitly if needed.
//
// The optional formats parameter specifies payload formats for decoding.
// makeSubscribeMessageHandler builds the *pahomqtt5.Publish message handler used
// by Subscribe and SubscribeStream. It applies ContentType negotiation,
// UserPropertyParams validation, security enforcement, observer calls, and
// tracing, then delivers the decoded value to fn.
//
// Separating handler creation from broker subscription lets SubscribeStream
// reuse the same validation logic without calling the broker.
func makeSubscribeMessageHandler[T any](
	ctx context.Context,
	handle *events.ChannelHandle[T],
	effectiveFmts []format.Format[T],
	fn func(context.Context, T) error,
	obs stats.Observer,
	opts SubscribeOptions,
) func(*pahomqtt5.Publish) {
	return func(msg *pahomqtt5.Publish) {
		start := time.Now()
		msgCtx := context.WithValue(ctx, contextKey{}, msg)
		if msg.Properties != nil && len(msg.Properties.User) > 0 {
			msgCtx = context.WithValue(msgCtx, userPropsKey{}, msg.Properties.User)
		}

		// Content-Type auto-selection: find a matching format by ContentType.
		var value T
		var decErr error
		ct := ""
		if msg.Properties != nil {
			ct = msg.Properties.ContentType
		}
		if ct != "" {
			for _, f := range effectiveFmts {
				if f.ContentType() == ct {
					value, decErr = f.Unmarshal(msg.Payload)
					goto decoded
				}
			}
		}
		// Fallback: use default format priority chain.
		if len(effectiveFmts) > 0 {
			value, decErr = effectiveFmts[0].Unmarshal(msg.Payload)
		} else {
			value, decErr = handle.Decode(msg.Payload)
		}

	decoded:
		if decErr != nil {
			stats.ReportErrors(obs, "payload", decErr)
			obs.RecordSubscribe(msg.Topic, false, time.Since(start))
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindDecode, Topic: msg.Topic, Err: decErr})
			}
			return
		}

		// Merge topic variables declared via events.NewTopicParam into the
		// SAME decoded value — additive, only runs when the channel has
		// merge-capable topic params (backward compatible: identical
		// behavior to today when none are declared). Mirrors REST's
		// request-merge wiring (see [rest.RouteHandle.DecodeMerged]).
		if mergeFields := handle.MergeFields(); len(mergeFields) > 0 {
			vars, varErr := TopicVarsFromMessage(handle, msg)
			if varErr != nil {
				stats.ReportErrors(obs, "topic_var", varErr)
				obs.RecordSubscribe(msg.Topic, false, time.Since(start))
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindDecode, Topic: msg.Topic, Err: varErr})
				}
				return
			}
			if mergeErr := codex.DecodeVars(&value, vars, mergeFields...); mergeErr != nil {
				stats.ReportErrors(obs, "topic_var", mergeErr)
				obs.RecordSubscribe(msg.Topic, false, time.Since(start))
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindDecode, Topic: msg.Topic, Err: mergeErr})
				}
				return
			}
		}

		// User Property param validation (runs before SecurityFunc).
		if propErr := validateUserProperties(msg, opts.UserPropertyParams); propErr != nil {
			obs.RecordValidationError("user_property", stats.ConstraintName(propErr), userPropertyName(propErr))
			obs.RecordSubscribe(msg.Topic, false, time.Since(start))
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindSecurity, Topic: msg.Topic, Err: propErr})
			}
			return
		}

		// Security enforcement.
		var secReqs []route.SecurityRequirement
		if handle.Descriptor.Subscribe != nil {
			secReqs = handle.Descriptor.Subscribe.Security
		}
		if secReqs == nil {
			secReqs = handle.GlobalSecurity
		}
		if len(secReqs) > 0 {
			// Built-in codec-based credential check — runs BEFORE the
			// optional custom SecurityFunc, mirroring adapters/nethttp's
			// validateSecurityCredentials + SecurityFunc ordering exactly.
			// A scheme with no Codec (or no
			// entry in handle.SecuritySchemes) is skipped — "nil Codec
			// means no format validation" (same contract as REST).
			schemeTypes := make(map[string]route.SecurityScheme, len(handle.SecuritySchemes))
			schemeCodecs := make(map[string]*codex.Codec[string], len(handle.SecuritySchemes))
			for name, s := range handle.SecuritySchemes {
				schemeTypes[name] = s.SecurityScheme
				schemeCodecs[name] = s.Codec
			}
			var userProps pahomqtt5.UserProperties
			if msg.Properties != nil {
				userProps = msg.Properties.User
			}
			if name, credErr := validateSecurityCredentials(userProps, secReqs, schemeTypes, schemeCodecs); credErr != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(msg.Topic, firstScheme(secReqs))
				}
				obs.RecordSubscribe(msg.Topic, false, time.Since(start))
				wrapped := events.SecurityCredentialError{Scheme: name, Err: credErr}
				if opts.OnError != nil {
					opts.OnError(SubscribeError{Kind: KindSecurity, Topic: msg.Topic, Err: wrapped})
				}
				return
			}
			if opts.SecurityFunc != nil {
				if err := opts.SecurityFunc(msgCtx, msg, secReqs); err != nil {
					if secObs, ok := obs.(stats.SecurityObserver); ok {
						secObs.RecordSecurityRejection(msg.Topic, firstScheme(secReqs))
					}
					obs.RecordSubscribe(msg.Topic, false, time.Since(start))
					if opts.OnError != nil {
						opts.OnError(SubscribeError{Kind: KindSecurity, Topic: msg.Topic, Err: events.SecurityError{Err: err}})
					}
					return
				}
			}
		}

		var spanCtx = msgCtx
		if to, ok := obs.(stats.TraceObserver); ok {
			spanCtx = to.StartSpan(msgCtx, "mqtt5.subscribe", msg.Topic)
		}
		fnErr := fn(spanCtx, value)
		if to, ok := obs.(stats.TraceObserver); ok {
			to.EndSpan(spanCtx, fnErr)
		}
		if fnErr != nil {
			stats.ReportErrors(obs, "topic_var", fnErr)
			obs.RecordSubscribe(msg.Topic, false, time.Since(start))
			if opts.OnError != nil {
				opts.OnError(SubscribeError{Kind: KindHandler, Topic: msg.Topic, Err: fnErr})
			}
			return
		}
		obs.RecordSubscribe(msg.Topic, true, time.Since(start))
	}
}

func Subscribe[T any](
	ctx context.Context,
	client MQTTClient,
	router MQTTRouter,
	handle *events.ChannelHandle[T],
	qos byte,
	fn func(context.Context, T) error,
	opts SubscribeOptions,
	formats ...format.Format[T],
) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}

	effectiveFmts := formats
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.SubscribeFormats
	}
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.Formats
	}

	router.RegisterHandler(handle.Topic,
		makeSubscribeMessageHandler(ctx, handle, effectiveFmts, fn, obs, opts))

	_, err := client.Subscribe(ctx, &pahomqtt5.Subscribe{
		Subscriptions: []pahomqtt5.SubscribeOptions{
			{Topic: handle.Topic, QoS: qos},
		},
	})
	if err != nil {
		router.UnregisterHandler(handle.Topic)
		return BrokerError{Op: "subscribe", Err: err}
	}
	return nil
}

// Publish encodes msg using handle's codec and publishes it to the broker.
//
// vars controls the topic:
//   - nil: use handle.Topic directly (static topics).
//   - non-nil: call handle.BuildTopic(vars) to resolve a template topic.
//
// MQTT 5 properties are set from [PublishOptions]: ContentType and UserProperties.
//
// The optional formats parameter specifies the payload format for encoding.
// Priority: call-time formats > handle.PublishFormats > handle.Formats > handle.Encode.
func Publish[T any](
	ctx context.Context,
	client MQTTClient,
	handle *events.ChannelHandle[T],
	qos byte,
	retained bool,
	msg T,
	vars map[string]string,
	opts PublishOptions,
	formats ...format.Format[T],
) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	start := time.Now()
	var err error
	if to, ok := obs.(stats.TraceObserver); ok {
		ctx = to.StartSpan(ctx, "mqtt5.publish", handle.Topic)
		defer func() { to.EndSpan(ctx, err) }()
	}

	topic := handle.Topic
	if vars != nil {
		var buildErr error
		topic, buildErr = handle.BuildTopic(vars)
		if buildErr != nil {
			err = buildErr
			reportTopicParamErrors(buildErr, obs)
			reportMissingTopicVarErrors(buildErr, obs)
			reportInvalidTopicErrors(buildErr, obs)
			obs.RecordPublish(handle.Topic, false, time.Since(start))
			return err
		}
	}

	effectiveFmts := formats
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.PublishFormats
	}
	if len(effectiveFmts) == 0 {
		effectiveFmts = handle.Formats
	}

	var payload []byte
	if len(effectiveFmts) > 0 {
		payload, err = effectiveFmts[0].Marshal(msg)
	} else {
		payload, err = handle.Encode(msg)
	}
	if err != nil {
		stats.ReportErrors(obs, "payload", err)
		obs.RecordPublish(topic, false, time.Since(start))
		return PublishEncodeError{Topic: topic, Err: err}
	}

	props := &pahomqtt5.PublishProperties{}
	if opts.ContentType != "" {
		props.ContentType = opts.ContentType
	}

	// Resolve security requirements and obtain credentials (client-side
	// mirror of makeSubscribeMessageHandler's server-side check, and of
	// [nethttp.Call]'s CredentialFunc handling).
	var secReqs []route.SecurityRequirement
	if handle.Descriptor.Publish != nil {
		secReqs = handle.Descriptor.Publish.Security
	}
	if secReqs == nil {
		secReqs = handle.GlobalSecurity
	}
	userProps := append(pahomqtt5.UserProperties(nil), opts.UserProperties...)
	var credProps []UserProperty
	if len(secReqs) > 0 && opts.CredentialFunc != nil {
		var credErr error
		credProps, credErr = opts.CredentialFunc(ctx, secReqs)
		if credErr != nil {
			obs.RecordPublish(topic, false, time.Since(start))
			return credErr
		}
		userProps = append(userProps, credProps...)
	}

	// Validate the outgoing credential FORMAT before publishing — the
	// client-side mirror of Subscribe's built-in check. Gated on
	// credProps != nil (CredentialFunc actually ran and returned
	// something), NOT on len(secReqs) > 0 alone — a nil CredentialFunc, or
	// one that deliberately returns (nil, nil) for "no credential needed",
	// must stay a non-error (see Round-93's REST regression fix, mirrored
	// here from day one).
	if len(secReqs) > 0 && credProps != nil {
		schemeTypes := make(map[string]route.SecurityScheme, len(handle.SecuritySchemes))
		schemeCodecs := make(map[string]*codex.Codec[string], len(handle.SecuritySchemes))
		for name, s := range handle.SecuritySchemes {
			schemeTypes[name] = s.SecurityScheme
			schemeCodecs[name] = s.Codec
		}
		if name, credErr := validateSecurityCredentials(userProps, secReqs, schemeTypes, schemeCodecs); credErr != nil {
			if secObs, ok := obs.(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(topic, firstScheme(secReqs))
			}
			obs.RecordPublish(topic, false, time.Since(start))
			return events.SecurityCredentialError{Scheme: name, Err: credErr}
		}
	}

	if len(userProps) > 0 {
		props.User = userProps
	}

	if _, err = client.Publish(ctx, &pahomqtt5.Publish{
		Topic:      topic,
		QoS:        qos,
		Retain:     retained,
		Payload:    payload,
		Properties: props,
	}); err != nil {
		obs.RecordPublish(topic, false, time.Since(start))
		return BrokerError{Op: "publish", Err: err}
	}
	obs.RecordPublish(topic, true, time.Since(start))
	return nil
}

// PublishHandle is the single-call convenience wrapper around [Publish]: it
// derives the topic vars map from msg automatically, using the channel's
// merge-capable topic params ([events.ChannelHandle.MergeFields] +
// [codex.EncodeVars]) — one struct in, no manual vars map, mirroring
// [nethttp.CallWithHandle]'s client-side convenience for REST.
//
// [Publish] remains available as the lower-level escape hatch for callers
// that build the vars map themselves (e.g. no merge fields declared, or
// vars come from a non-struct source).
//
//	err := mqtt5.PublishHandle(ctx, client, sensorChannel, 1, false, reading, mqtt5.PublishOptions{})
func PublishHandle[T any](
	ctx context.Context,
	client MQTTClient,
	handle *events.ChannelHandle[T],
	qos byte,
	retained bool,
	msg T,
	opts PublishOptions,
	formats ...format.Format[T],
) error {
	vars, err := codex.EncodeVars(msg, handle.MergeFields()...)
	if err != nil {
		return err
	}
	if len(vars) == 0 {
		vars = nil
	}
	return Publish(ctx, client, handle, qos, retained, msg, vars, opts, formats...)
}

// firstScheme returns the first scheme name from the security requirements.
func firstScheme(reqs []route.SecurityRequirement) string {
	for _, req := range reqs {
		for name := range req {
			return name
		}
	}
	return ""
}

// validateUserProperties checks the incoming message's User Properties against
// the registered [UserPropertyParam] slice. Returns nil when all params pass,
// [MissingUserPropertyError] when a required property is absent, or
// [UserPropertyError] when a codec check fails.
func validateUserProperties(msg *pahomqtt5.Publish, params []UserPropertyParam) error {
	if len(params) == 0 {
		return nil
	}
	// Build a lookup map from the message's User Properties.
	props := make(map[string]string, 8)
	if msg.Properties != nil {
		for _, p := range msg.Properties.User {
			props[p.Key] = p.Value
		}
	}
	for _, param := range params {
		val, ok := props[param.Name]
		if !ok {
			if param.Required {
				return MissingUserPropertyError{Name: param.Name}
			}
			continue // optional and absent: skip codec check
		}
		if param.Codec != nil {
			if err := param.Codec.Validate(val); err != nil {
				return UserPropertyError{Name: param.Name, Value: val, Err: err}
			}
		}
	}
	return nil
}

// reportTopicParamErrors extracts the failing topic variable from a [events.TopicParamError]
// and reports it to obs with location "topic_var".
func reportTopicParamErrors(err error, obs stats.Observer) {
	var pe events.TopicParamError
	if !errors.As(err, &pe) {
		return
	}
	obs.RecordValidationError("topic_var", stats.ConstraintName(pe.Err), pe.Name)
}

// reportMissingTopicVarErrors extracts the missing variable name from a [events.MissingTopicVarError]
// and reports it to obs with location "topic_var" and constraint "required".
func reportMissingTopicVarErrors(err error, obs stats.Observer) {
	var me events.MissingTopicVarError
	if !errors.As(err, &me) {
		return
	}
	obs.RecordValidationError("topic_var", "required", me.Name)
}

// reportInvalidTopicErrors extracts the constraint from an [events.InvalidTopicError]
// and reports it to obs with location "topic".
func reportInvalidTopicErrors(err error, obs stats.Observer) {
	var ie events.InvalidTopicError
	if !errors.As(err, &ie) {
		return
	}
	obs.RecordValidationError("topic", stats.ConstraintName(ie.Err), "")
}

// userPropertyName extracts the property key from a User Property error for
// observer reporting.
func userPropertyName(err error) string {
	if e, ok := err.(UserPropertyError); ok {
		return e.Name
	}
	if e, ok := err.(MissingUserPropertyError); ok {
		return e.Name
	}
	return ""
}
