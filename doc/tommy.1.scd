tommy(1)

# NAME

tommy - TOML library and CLI for Go

# SYNOPSIS

*tommy* <command> [<args>...]

# DESCRIPTION

*tommy* is a TOML library for Go that preserves comments and formatting on
round-trip. It ships with a CLI that exposes two entry points: a
format-preserving formatter and a code generator that emits type-safe
*Decode*/*Encode* methods from struct annotations.

The library itself (not documented here) provides a CST-based parser, a
format-preserving document API, and reflection-based marshal/unmarshal. See
the project *README* for library usage.

# COMMANDS

*fmt* [*--check*] <files...|->
	Format TOML files in place, or check whether they are formatted.
	See *tommy-fmt*(1).

*generate*
	Generate type-safe *Decode*/*Encode* methods for annotated structs.
	Invoked via *//go:generate tommy generate*. See *tommy-generate*(1).

Any other first argument is rejected with *unknown command*.

# STRUCT TAGS

Fields opted into codegen use the *toml* struct tag. For the key name,
*omitempty* modifier, and *multiline* modifier, see *tommy-struct-tags*(7).

# EXIT STATUS

*0*
	Success.

*1*
	Unknown command, missing arguments, or any error reported by the
	subcommand (file I/O, parse failure, codegen failure, unformatted file
	under *--check*).

# SEE ALSO

*tommy-fmt*(1), *tommy-generate*(1), *tommy-struct-tags*(7)
