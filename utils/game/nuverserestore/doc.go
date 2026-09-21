// Package nuverserestore loads Nuverse AVSC schemas and restores positional
// Suite records, MYSEKAI harvest records, and column-oriented compact data.
//
// SuiteRestorer preserves the established permissive Suite conversion contract.
// MysekaiRestorer validates typed harvest layouts and performs copy-on-write
// restoration with per-region schema fingerprints. CompactOptions controls
// column and enum expansion. These formats share a package, while retaining
// their distinct validation, mutation, and missing-value semantics.
package nuverserestore
