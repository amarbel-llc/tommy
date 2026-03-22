package tommy

type TOMLUnmarshaler interface {
	UnmarshalTOML(data any) error
}

type TOMLMarshaler interface {
	MarshalTOML() (any, error)
}
