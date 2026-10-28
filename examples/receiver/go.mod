module github.com/joaomouravs/carteiro/examples/receiver

go 1.27.0

// The tests sign with Carteiro's own code, from this repository.
replace github.com/joaomouravs/carteiro => ../..

require (
	github.com/joaomouravs/carteiro v0.0.0-00010101000000-000000000000
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1
)
