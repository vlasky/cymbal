package index

import "testing"

func TestClassifyPath(t *testing.T) {
	cases := []struct {
		path string
		want PathClass
	}{
		// Confident test files, by language convention.
		{"index/store_test.go", PathClassTest},
		{"pkg/foo/bar_test.go", PathClassTest},
		{"tests/test_thing.py", PathClassTest},
		{"app/models/user_spec.rb", PathClassTest},
		{"src/components/Button.test.tsx", PathClassTest},
		{"src/components/Button.spec.ts", PathClassTest},
		{"conftest.py", PathClassTest},
		{"test_main.py", PathClassTest},
		{"src/test/java/com/x/FooTest.java", PathClassTest},
		{"src/test/java/com/x/FooIT.java", PathClassTest},
		{"Domain/Orders/OrderServiceTests.cs", PathClassTest},
		// Test directories.
		{"spec/models/user.rb", PathClassTest},
		{"__tests__/render.js", PathClassTest},
		{"internal/index/testdata/sample.go", PathClassTest},
		{"e2e/login.ts", PathClassTest},
		{"tests/helpers.rb", PathClassTest},
		// Module-flavoured JS/TS test extensions, PHPUnit, Kotest, Django.
		{"src/util.test.cjs", PathClassTest},
		{"src/util.spec.mts", PathClassTest},
		{"src/Service/OrderServiceTest.php", PathClassTest},
		{"src/main/kotlin/OrderSpec.kt", PathClassTest},
		{"app/tests.py", PathClassTest},
		{"polls/tests.py", PathClassTest},

		// Windows rel paths (the walker stores native separators): anchoring
		// must survive backslashes, and the bare-convention guard ("Test.java"
		// with no class-name prefix is production) must not invert.
		{`tests\helpers.rb`, PathClassTest},
		{`spec\models\user.rb`, PathClassTest},
		{`__tests__\render.js`, PathClassTest},
		{`src\test\java\com\x\Foo.java`, PathClassTest},
		{`pkg\foo\bar_test.go`, PathClassTest},
		{`nested\dir\conftest.py`, PathClassTest},
		{`src\Test.java`, PathClassProduction},
		{`lib\Contestant.go`, PathClassProduction},

		// False-positive guards: production code that merely contains
		// "test"/"spec" as a substring must NOT be classified as test.
		{"src/ApplicationCore/Specifications/CatalogFilterSpecification.cs", PathClassProduction},
		{"app/controllers/admin/tests_controller.rb", PathClassProduction},
		{"src/main/java/org/app/Latest.java", PathClassProduction},
		{"src/main/java/org/app/Manifest.java", PathClassProduction},
		{"lib/Contestant.go", PathClassProduction},
		{"src/Greatest.cs", PathClassProduction},

		// Plain production.
		{"index/store.go", PathClassProduction},
		{"app/models/user.rb", PathClassProduction},
		{"src/components/Button.tsx", PathClassProduction},
		{"cmd/impact.go", PathClassProduction},

		// Ambiguous → unknown (kept by --no-tests).
		{"app/javascript/testing/rendering.tsx", PathClassUnknown},
		{"internal/fixtures/data.go", PathClassUnknown},
		{"examples/demo.go", PathClassUnknown},
		{"src/mocks/server.ts", PathClassUnknown},
		{"pkg/testutil/helper.go", PathClassUnknown},

		// Degenerate.
		{"", PathClassUnknown},
	}

	for _, c := range cases {
		if got := ClassifyPath(c.path); got != c.want {
			t.Errorf("ClassifyPath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestClassifierTestPathPatterns(t *testing.T) {
	// nil classifier == built-ins only.
	var nilCl *Classifier
	if got := nilCl.Classify("qa/scenario.go"); got != PathClassProduction {
		t.Errorf("nil classifier Classify(qa/scenario.go) = %q, want production", got)
	}
	if NewClassifier(nil) != nil || NewClassifier([]string{"", "  "}) != nil {
		t.Error("NewClassifier with no usable patterns should return nil")
	}

	cl := NewClassifier([]string{"qa/", "**/*_it.go"})
	cases := []struct {
		path string
		want PathClass
	}{
		{"qa/scenario.go", PathClassTest},          // substring pattern
		{"services/qa/checks.rb", PathClassTest},   // substring anywhere
		{"pkg/orders/orders_it.go", PathClassTest}, // glob with **
		{"pkg/orders/orders.go", PathClassProduction},
		{"index/store_test.go", PathClassTest}, // built-ins still apply
		{`qa\scenario.go`, PathClassTest},      // windows separators normalized
		{"", PathClassUnknown},
	}
	for _, c := range cases {
		if got := cl.Classify(c.path); got != c.want {
			t.Errorf("Classify(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
