package detect

import "testing"

func TestInferLayerFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected FileLayerHint
	}{
		{"src/controllers/user.go", LayerController},
		{"app/handlers/auth.js", LayerHandler},
		{"internal/service/payment.go", LayerService},
		{"pkg/repository/users.go", LayerRepository},
		{"models/user.py", LayerModel},
		{"middleware/auth.ts", LayerMiddleware},
		{"api/v1/routes.go", LayerAPI},
		{"db/migrations/001.sql", LayerMigration}, // migration is more specific than db
		{"migrations/20210101_create_users.sql", LayerMigration},
		{"config/settings.yml", LayerConfig},
		{"tests/unit/auth_test.go", LayerTest},
		{"__tests__/auth.test.js", LayerTest},
		{"utils/helpers.go", LayerUtil},
		{"lib/common/strings.go", LayerUtil},
		{"random/path/file.go", LayerUnknown},
	}

	for _, tc := range tests {
		result := InferLayerFromPath(tc.path)
		if result != tc.expected {
			t.Errorf("InferLayerFromPath(%q) = %q, expected %q", tc.path, result, tc.expected)
		}
	}
}

func TestInferSymbolRole(t *testing.T) {
	tests := []struct {
		name     string
		expected SymbolRole
	}{
		{"UserHandler", RoleHandler},
		{"AuthController", RoleController},
		{"InputValidator", RoleValidator},
		{"JSONSerializer", RoleSerializer},
		{"UserFactory", RoleFactory},
		{"QueryBuilder", RoleBuilder},
		{"StringHelper", RoleHelper},
		{"ClickCallback", RoleCallback},
		{"AuthHook", RoleHook},
		{"LoggingMiddleware", RoleMiddleware},
		{"handleClick", RoleHandler},
		{"validateInput", RoleValidator},
		{"createUser", RoleFactory},
		{"buildQuery", RoleBuilder},
		{"useAuth", RoleHook},
		{"onClick", RoleCallback},
		{"processData", RoleUnknown},
	}

	for _, tc := range tests {
		result := InferSymbolRole(tc.name)
		if result != tc.expected {
			t.Errorf("InferSymbolRole(%q) = %q, expected %q", tc.name, result, tc.expected)
		}
	}
}

func TestInferConfigKeyCategory(t *testing.T) {
	tests := []struct {
		key      string
		expected ConfigKeyCategory
	}{
		{"featureEnabled", ConfigFeatureFlag},
		{"isFeatureDisabled", ConfigFeatureFlag},
		{"featureToggle", ConfigFeatureFlag},
		{"requestTimeout", ConfigTimeout},
		{"sessionTTL", ConfigTimeout},
		{"cacheExpire", ConfigTimeout}, // expire pattern without token
		{"maxConnections", ConfigLimit},
		{"minPoolSize", ConfigLimit},
		{"rateThreshold", ConfigLimit},
		{"retryDelayMs", ConfigRetry}, // retry without count/max
		{"backoffMs", ConfigRetry},
		{"apiUrl", ConfigEndpoint},
		{"serverHost", ConfigEndpoint},
		{"listenPort", ConfigEndpoint},
		{"apiSecret", ConfigCredential},
		{"authToken", ConfigCredential},
		{"dbPassword", ConfigCredential},
		{"someSetting", ConfigGeneral},
	}

	for _, tc := range tests {
		result := InferConfigKeyCategory(tc.key)
		if result != tc.expected {
			t.Errorf("InferConfigKeyCategory(%q) = %q, expected %q", tc.key, result, tc.expected)
		}
	}
}

func TestInferModuleFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"src/auth/login.js", "Auth"},
		{"internal/payment/stripe.go", "Payment"},
		{"pkg/users/repository.go", "Users"},
		{"app/api/v1/users.go", "V1"},
		{"handlers/auth.go", "Handlers"},
		{"utils.go", "Utils"},
		{"", "General"}, // empty path returns General as fallback
	}

	for _, tc := range tests {
		result := InferModuleFromPath(tc.path)
		if result != tc.expected {
			t.Errorf("InferModuleFromPath(%q) = %q, expected %q", tc.path, result, tc.expected)
		}
	}
}

func TestEnrichSignalWithMetadata(t *testing.T) {
	sig := &ChangeSignal{
		Category: FunctionAdded,
		Evidence: ExtendedEvidence{
			FileRanges: []FileRange{{Path: "src/handlers/auth.js"}},
			Symbols:    []string{"name:handleLogin"},
		},
	}

	EnrichSignalWithMetadata(sig)

	if sig.Evidence.Layer != LayerHandler {
		t.Errorf("expected layer %q, got %q", LayerHandler, sig.Evidence.Layer)
	}
	if sig.Evidence.Module != "Handlers" {
		t.Errorf("expected module %q, got %q", "Handlers", sig.Evidence.Module)
	}
	if sig.Evidence.SymbolMeta == nil {
		t.Fatal("expected SymbolMeta to be set")
	}
	if sig.Evidence.SymbolMeta.Role != RoleHandler {
		t.Errorf("expected role %q, got %q", RoleHandler, sig.Evidence.SymbolMeta.Role)
	}
}

func TestNewChangeSignal_WithMetadata(t *testing.T) {
	ct := &ChangeType{
		Category: FunctionAdded,
		Evidence: Evidence{
			FileRanges: []FileRange{{Path: "src/controllers/user.go"}},
			Symbols:    []string{"name:CreateUserHandler"},
		},
	}

	sig := NewChangeSignal(ct)

	// Check metadata was auto-enriched
	if sig.Evidence.Layer != LayerController {
		t.Errorf("expected layer %q, got %q", LayerController, sig.Evidence.Layer)
	}
	if sig.Evidence.SymbolMeta == nil {
		t.Fatal("expected SymbolMeta to be set")
	}
	if sig.Evidence.SymbolMeta.Role != RoleHandler {
		t.Errorf("expected role %q, got %q", RoleHandler, sig.Evidence.SymbolMeta.Role)
	}
}

func TestHasTag(t *testing.T) {
	sig := &ChangeSignal{
		Tags: []string{"api", "breaking"},
	}

	if !sig.HasTag("api") {
		t.Error("expected HasTag('api') to return true")
	}
	if !sig.HasTag("breaking") {
		t.Error("expected HasTag('breaking') to return true")
	}
	if sig.HasTag("config") {
		t.Error("expected HasTag('config') to return false")
	}
}

func TestIsBreaking(t *testing.T) {
	breaking := &ChangeSignal{Tags: []string{"breaking"}}
	notBreaking := &ChangeSignal{Tags: []string{"api"}}

	if !breaking.IsBreaking() {
		t.Error("expected IsBreaking() to return true")
	}
	if notBreaking.IsBreaking() {
		t.Error("expected IsBreaking() to return false")
	}
}

func TestIsAPIChange(t *testing.T) {
	api := &ChangeSignal{Tags: []string{"api"}}
	notAPI := &ChangeSignal{Tags: []string{"config"}}

	if !api.IsAPIChange() {
		t.Error("expected IsAPIChange() to return true")
	}
	if notAPI.IsAPIChange() {
		t.Error("expected IsAPIChange() to return false")
	}
}

func TestConvertToSignals(t *testing.T) {
	changeTypes := []*ChangeType{
		{Category: FunctionAdded, Evidence: Evidence{FileRanges: []FileRange{{Path: "a.js"}}}},
		{Category: FunctionRemoved, Evidence: Evidence{FileRanges: []FileRange{{Path: "b.js"}}}},
	}

	signals := ConvertToSignals(changeTypes)

	if len(signals) != 2 {
		t.Fatalf("expected 2 signals, got %d", len(signals))
	}
	if signals[0].Category != FunctionAdded {
		t.Errorf("expected FunctionAdded, got %s", signals[0].Category)
	}
	if signals[1].Category != FunctionRemoved {
		t.Errorf("expected FunctionRemoved, got %s", signals[1].Category)
	}
}

func TestEnrichConfigSignal_FeatureFlag(t *testing.T) {
	sig := &ChangeSignal{
		Category: JSONValueChanged,
		Evidence: ExtendedEvidence{
			Symbols:     []string{"featureEnabled"},
			BeforeValue: "false",
			AfterValue:  "true",
		},
	}

	EnrichConfigSignal(sig)

	if sig.Category != FeatureFlagChanged {
		t.Errorf("expected FeatureFlagChanged, got %s", sig.Category)
	}
	if sig.Evidence.ConfigChange == nil {
		t.Fatal("expected ConfigChange to be set")
	}
	if sig.Evidence.ConfigChange.KeyCategory != ConfigFeatureFlag {
		t.Errorf("expected ConfigFeatureFlag, got %s", sig.Evidence.ConfigChange.KeyCategory)
	}
	if !sig.HasTag("feature-flag") {
		t.Error("expected feature-flag tag")
	}
}

func TestEnrichConfigSignal_Timeout(t *testing.T) {
	sig := &ChangeSignal{
		Category: YAMLValueChanged,
		Evidence: ExtendedEvidence{
			Symbols:     []string{"requestTimeout"},
			BeforeValue: "5000",
			AfterValue:  "10000",
		},
	}

	EnrichConfigSignal(sig)

	if sig.Category != TimeoutChanged {
		t.Errorf("expected TimeoutChanged, got %s", sig.Category)
	}
	if !sig.HasTag("tuning") {
		t.Error("expected tuning tag")
	}
}

func TestEnrichConfigSignal_Credential(t *testing.T) {
	sig := &ChangeSignal{
		Category: JSONValueChanged,
		Evidence: ExtendedEvidence{
			Symbols: []string{"apiSecret"},
		},
	}

	EnrichConfigSignal(sig)

	if sig.Category != CredentialChanged {
		t.Errorf("expected CredentialChanged, got %s", sig.Category)
	}
	if !sig.HasTag("security") {
		t.Error("expected security tag")
	}
}

func TestEnrichConfigSignal_NonConfig(t *testing.T) {
	sig := &ChangeSignal{
		Category: JSONValueChanged,
		Evidence: ExtendedEvidence{
			Symbols: []string{"userProfile.name"},
		},
	}

	originalCategory := sig.Category
	EnrichConfigSignal(sig)

	// Should not upgrade a generic field
	if sig.Category != originalCategory {
		t.Errorf("expected category to remain %s, got %s", originalCategory, sig.Category)
	}
}
