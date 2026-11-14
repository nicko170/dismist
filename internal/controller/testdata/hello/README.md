# Golden ep-auth hellos

Sanitised copies of the `ep-auth` hellos captured from an AP33 on firmware
0.14 and 0.15 (design §5.4). Field names, field order and JSON types are
kept exactly; every value is replaced with synthetic data (locally
administered MAC-style ID, documentation address 192.0.2.10, an invented
serial, a fixed QC challenge and counter). They contain no captured
secrets or identities.

The tests in `hello_test.go` mutate every extra field (wrong type,
missing, `null`) and assert that authentication succeeds for all of them.
