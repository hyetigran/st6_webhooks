// Package properties holds the seeded-randomness support for `make
// properties` (PRD §8: "seeds are logged" — every property-test failure
// must be reproducible by rerunning with the same seed). Unlike Node (which
// needed a hand-written mulberry32 PRNG, since JS has no built-in seedable
// generator), Go's math/rand is already a real seedable PRNG — this package
// only needs to supply the seed itself, logged and env-overridable.
package properties

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
)

// GetTestSeed returns a reproducible seed for one property test, logging it
// so a failure can be rerun exactly via PROPERTY_TEST_SEED=<seed>. Seed
// math/rand.New(rand.NewSource(seed)) with the result.
func GetTestSeed(label string) int64 {
	seed := rand.Int63()
	if fromEnv := os.Getenv("PROPERTY_TEST_SEED"); fromEnv != "" {
		parsed, err := strconv.ParseInt(fromEnv, 10, 64)
		if err != nil {
			panic(fmt.Sprintf("PROPERTY_TEST_SEED must be an integer, got %q", fromEnv))
		}
		seed = parsed
	}
	fmt.Printf("[property test seed] %s: %d (rerun with PROPERTY_TEST_SEED=%d to reproduce)\n", label, seed, seed)
	return seed
}
