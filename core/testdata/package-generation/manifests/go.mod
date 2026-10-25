// Client module settings must survive dependency changes.
module example.com/client

go 1.25.0

require (
	example.com/shared v1.2.0 // indirect
	example.com/unchanged v1.0.0
)

replace example.com/local => ./libs/local

exclude example.com/blocked v1.0.0
