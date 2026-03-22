package tommy

import "testing"

type mockUnmarshaler struct{ called bool }

func (m *mockUnmarshaler) UnmarshalTOML(data any) error {
	m.called = true
	return nil
}

type mockMarshaler struct{}

func (m *mockMarshaler) MarshalTOML() (any, error) {
	return "test", nil
}

func TestInterfaceCompliance(t *testing.T) {
	var u TOMLUnmarshaler = &mockUnmarshaler{}
	if err := u.UnmarshalTOML("hello"); err != nil {
		t.Fatal(err)
	}

	var m TOMLMarshaler = &mockMarshaler{}
	v, err := m.MarshalTOML()
	if err != nil {
		t.Fatal(err)
	}
	if v != "test" {
		t.Fatalf("expected %q, got %q", "test", v)
	}
}
