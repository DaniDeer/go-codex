package codex

// Must returns v if err is nil, and panics with err otherwise.
//
// It follows the same convention as template.Must and regexp.MustCompile:
// use it to wrap any (T, error) call where failure is a programming error,
// not a recoverable runtime condition.
//
// Typical uses include package-level validated constants and test data setup:
//
//	var defaultEmail = codex.Must(emailCodec.New(Email("noreply@example.com")))
//	got := codex.Must(emailCodec.Decode("user@example.com"))
func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// Must2 is [Must] for a (A, B, error) triple — e.g. the port + handle pair
// returned by ports' protocol-named convenience constructors
// (ports.NewRestPort, ports.NewReqReplyPort, ports.NewMCPPort, ...):
//
//	Readings, readingsHandle := codex.Must2(ports.NewRestPort[Req, Resp](
//	    "rest/readings", reqCodec, respCodec, pattern, opts))
func Must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}
