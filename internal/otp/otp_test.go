package otp

import "testing"

func TestExtract(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want string
	}{
		{"twilio", "Your ExampleApp verification code is: 123456", "123456"},
		{"stripe", "Your Stripe verification code is 482731", "482731"},
		{"norwegian after", "Bekreftelseskoden din er 482731.", "482731"},
		{"norwegian before", "482731 er bekreftelseskoden din.", "482731"},
		{"domain bound", "747723 is your authentication code.\n\n@example.com #747723", "747723"},
		{"apple iframe", "Your code is 123456.\n\n@example.com #123456 %iframe.example.org", "123456"},
		{"wicg embedded", "Your code is A1B2C3.\n\n@example.com #A1B2C3 @auth.example.org", "A1B2C3"},
		{"google hash", "Your ExampleApp code is: 123ABC78\nFA+9qCX9VSu", "123ABC78"},
		{"strong alphanumeric", "Your authentication code is A1B2C3", "A1B2C3"},
		{"weak numeric", "Your code is 654321", "654321"},
		{"otp", "OTP: 193847", "193847"},
		{"promotion", "Use code SAVE20 for 20% off.", ""},
		{"order", "Your order 482731 has shipped.", ""},
		{"amount", "Pay EUR 39.99; card ending 1234.", ""},
		{"url", "Your verification code is at https://example.com/123456", ""},
		{"email", "Your verification code is user1234@example.com", ""},
		{"date", "Your verification code expires on 20260820", ""},
		{"phone", "If your verification code fails, call 12345678.", ""},
		{"ambiguous", "Verification code 1234 or 5678", ""},
		{"too short", "Your verification code is 123", ""},
		{"too long", "Your verification code is 12345678901", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := Extract(test.body)
			if got != test.want || ok != (test.want != "") {
				t.Fatalf("Extract() = %q, %v; want %q", got, ok, test.want)
			}
		})
	}
}
