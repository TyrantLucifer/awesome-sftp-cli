package contracttest

import "testing"

type fixtureFactoryFunc func(*testing.T) Fixture

func (factory fixtureFactoryFunc) New(t *testing.T) Fixture {
	return factory(t)
}

func TestFactoryUsesExplicitFixture(t *testing.T) {
	var _ Factory = fixtureFactoryFunc(nil)
}
