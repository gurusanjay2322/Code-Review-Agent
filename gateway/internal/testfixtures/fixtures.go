// Package testfixtures holds a small PR diff shared by tests across packages.
package testfixtures

import "strings"

const Path = "store/user.go"

// Content is store/user.go at the PR head. Line numbers referenced by tests:
// 5 = added "fmt" import, 9 = added Sprintf query (SQL injection), 10 = added QueryRow(q).
var Content = strings.Join([]string{
	"package store",      // 1
	"",                   // 2
	"import (",           // 3
	"\t\"database/sql\"", // 4
	"\t\"fmt\"",          // 5
	")",                  // 6
	"",                   // 7
	"func FindUser(db *sql.DB, name string) (*User, error) {",                    // 8
	"\tq := fmt.Sprintf(\"SELECT id, name FROM users WHERE name = '%s'\", name)", // 9
	"\trow := db.QueryRow(q)", // 10
	"\tvar u User",            // 11
	"\tif err := row.Scan(&u.ID, &u.Name); err != nil {", // 12
	"\t\treturn nil, err",                                // 13
	"\t}",                                                // 14
	"\treturn &u, nil",                                   // 15
	"}",                                                  // 16
}, "\n")

var Patch = strings.Join([]string{
	"@@ -3,10 +3,12 @@",
	" import (",
	" \t\"database/sql\"",
	"+\t\"fmt\"",
	" )",
	" ",
	" func FindUser(db *sql.DB, name string) (*User, error) {",
	"-\trow := db.QueryRow(\"SELECT id, name FROM users WHERE name = $1\", name)",
	"+\tq := fmt.Sprintf(\"SELECT id, name FROM users WHERE name = '%s'\", name)",
	"+\trow := db.QueryRow(q)",
	" \tvar u User",
	" \tif err := row.Scan(&u.ID, &u.Name); err != nil {",
	" \t\treturn nil, err",
	" \t}",
}, "\n")

// SecondHunkPatch has two separate hunks, for same-hunk and window-merging checks.
var SecondHunkPatch = strings.Join([]string{
	"@@ -1,3 +1,4 @@",
	" a",
	"+b",
	" c",
	" d",
	"@@ -50,3 +51,4 @@",
	" x",
	"+y",
	" z",
	" w",
}, "\n")
