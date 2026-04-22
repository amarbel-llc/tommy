tommy-fmt(1)

# NAME

tommy-fmt - format TOML files while preserving comments

# SYNOPSIS

*tommy fmt* [*--check*] <files...|->

# DESCRIPTION

*tommy fmt* reads each input, parses it into a CST, applies a fixed set of
whitespace normalizations, and writes the result back.

Because the formatter operates on the CST rather than a lossy value tree,
comments, key ordering, and inline formatting choices that are not
explicitly normalized are preserved byte-for-byte.

If an input fails to parse, it is written back unchanged (no error is
reported) and the next file is processed. If file I/O fails, an error is
printed to stderr and *tommy fmt* exits non-zero after all files have been
attempted.

# ARGUMENTS

<files...>
	One or more paths. Each file is read, formatted, and written back in
	place.

*-*
	Read from standard input and write the formatted result to standard
	output. Use this for editor integration.

# OPTIONS

*--check*
	Do not modify any file. For each input, exit status *1* is reported if
	the formatted output would differ from the input. Useful in CI to
	enforce formatting.

# FORMATTING RULES

The formatter applies these rules, and only these rules:

. Exactly one space before and after *=* in every key-value pair.
. Exactly one space before an inline comment that follows a value on the
  same line.
. Trailing whitespace before every newline is removed, including trailing
  whitespace inside comment text.
. Exactly one blank line before any line starting with *[* (table or
  array-table header), except at the start of the file.
. Exactly one trailing newline at end of file.

Rules not listed above are deliberately left alone. In particular,
indentation inside arrays and inline tables, alignment of consecutive
assignments, and the choice of single-line vs. multi-line string quoting
are preserved.

# EXIT STATUS

*0*
	All inputs succeeded. Under *--check*, all inputs were already
	formatted.

*1*
	One or more inputs failed (I/O error), or under *--check* one or more
	inputs were not formatted.

# EXAMPLES

Format every TOML file in the current tree:

```
find . -name '*.toml' -print0 | xargs -0 tommy fmt
```

Check formatting in CI and fail if anything drifts:

```
tommy fmt --check config/*.toml
```

Format standard input (editor integration):

```
cat pyproject.toml | tommy fmt -
```

# SEE ALSO

*tommy*(1), *tommy-generate*(1)
