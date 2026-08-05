package agenteval

import "testing"

func validCase() DecisionCase {
	return DecisionCase{
		ID: "case-1", RunID: "run_1", DecisionNo: 2,
		ExpectedActionName: "search_evidence",
	}
}

func TestDecisionSuiteValidateAcceptsWellFormedSuite(t *testing.T) {
	suite := DecisionSuite{SchemaVersion: 1, ID: "suite-1", Cases: []DecisionCase{validCase()}}
	if err := suite.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDecisionSuiteValidateRejectsWrongSchemaVersion(t *testing.T) {
	suite := DecisionSuite{SchemaVersion: 2, ID: "suite-1", Cases: []DecisionCase{validCase()}}
	if err := suite.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for schema_version != 1")
	}
}

func TestDecisionSuiteValidateRejectsDuplicateCaseIDs(t *testing.T) {
	first, second := validCase(), validCase()
	suite := DecisionSuite{SchemaVersion: 1, ID: "suite-1", Cases: []DecisionCase{first, second}}
	if err := suite.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for duplicate case ids")
	}
}

func TestDecisionSuiteValidateRejectsMissingExpectedActionName(t *testing.T) {
	brokenCase := validCase()
	brokenCase.ExpectedActionName = ""
	suite := DecisionSuite{SchemaVersion: 1, ID: "suite-1", Cases: []DecisionCase{brokenCase}}
	if err := suite.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for missing expected_action_name")
	}
}

func TestDecisionSuiteValidateRejectsDecisionNoBelowOne(t *testing.T) {
	brokenCase := validCase()
	brokenCase.DecisionNo = 0
	suite := DecisionSuite{SchemaVersion: 1, ID: "suite-1", Cases: []DecisionCase{brokenCase}}
	if err := suite.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for decision_no < 1")
	}
}

func TestDecisionSuiteValidateRejectsRequiredKeysSubsetWithoutKeys(t *testing.T) {
	brokenCase := validCase()
	brokenCase.ComparisonMode = ComparisonRequiredKeysSubset
	suite := DecisionSuite{SchemaVersion: 1, ID: "suite-1", Cases: []DecisionCase{brokenCase}}
	if err := suite.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for required_keys_subset mode without required_input_keys")
	}
}

func TestDecisionSuiteValidateRejectsEmptyCases(t *testing.T) {
	suite := DecisionSuite{SchemaVersion: 1, ID: "suite-1"}
	if err := suite.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for empty Cases")
	}
}
