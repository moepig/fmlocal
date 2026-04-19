package e2e

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/gamelift"
	"github.com/aws/aws-sdk-go-v2/service/gamelift/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- DescribeMatchmakingConfigurations ---

func TestE2E_DescribeMatchmakingConfigurations_All(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStack(t, false)
	client := newGameLiftClient(t, st.httpSrv.URL)

	out, err := client.DescribeMatchmakingConfigurations(context.Background(), &gamelift.DescribeMatchmakingConfigurationsInput{})
	require.NoError(t, err)
	require.Len(t, out.Configurations, 1)
	assert.Equal(t, "cfg", aws.ToString(out.Configurations[0].Name))
	assert.Equal(t, types.FlexMatchModeStandalone, out.Configurations[0].FlexMatchMode)
}

func TestE2E_DescribeMatchmakingConfigurations_FilterByName(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStack(t, false)
	client := newGameLiftClient(t, st.httpSrv.URL)

	out, err := client.DescribeMatchmakingConfigurations(context.Background(), &gamelift.DescribeMatchmakingConfigurationsInput{
		Names: []string{"cfg"},
	})
	require.NoError(t, err)
	require.Len(t, out.Configurations, 1)
	assert.Equal(t, "cfg", aws.ToString(out.Configurations[0].Name))

	out, err = client.DescribeMatchmakingConfigurations(context.Background(), &gamelift.DescribeMatchmakingConfigurationsInput{
		Names: []string{"ghost"},
	})
	require.NoError(t, err)
	assert.Len(t, out.Configurations, 0)
}

func TestE2E_DescribeMatchmakingConfigurations_FilterByRuleSet(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStack(t, false)
	client := newGameLiftClient(t, st.httpSrv.URL)

	out, err := client.DescribeMatchmakingConfigurations(context.Background(), &gamelift.DescribeMatchmakingConfigurationsInput{
		RuleSetName: aws.String("1v1"),
	})
	require.NoError(t, err)
	require.Len(t, out.Configurations, 1)

	out, err = client.DescribeMatchmakingConfigurations(context.Background(), &gamelift.DescribeMatchmakingConfigurationsInput{
		RuleSetName: aws.String("other"),
	})
	require.NoError(t, err)
	assert.Len(t, out.Configurations, 0)
}

// --- DescribeMatchmakingRuleSets ---

func TestE2E_DescribeMatchmakingRuleSets_All(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStack(t, false)
	client := newGameLiftClient(t, st.httpSrv.URL)

	out, err := client.DescribeMatchmakingRuleSets(context.Background(), &gamelift.DescribeMatchmakingRuleSetsInput{})
	require.NoError(t, err)
	require.Len(t, out.RuleSets, 1)
	assert.Equal(t, "1v1", aws.ToString(out.RuleSets[0].RuleSetName))
	assert.NotEmpty(t, aws.ToString(out.RuleSets[0].RuleSetBody))
}

func TestE2E_DescribeMatchmakingRuleSets_FilterByName(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStack(t, false)
	client := newGameLiftClient(t, st.httpSrv.URL)

	out, err := client.DescribeMatchmakingRuleSets(context.Background(), &gamelift.DescribeMatchmakingRuleSetsInput{
		Names: []string{"1v1"},
	})
	require.NoError(t, err)
	require.Len(t, out.RuleSets, 1)

	out, err = client.DescribeMatchmakingRuleSets(context.Background(), &gamelift.DescribeMatchmakingRuleSetsInput{
		Names: []string{"ghost"},
	})
	require.NoError(t, err)
	assert.Len(t, out.RuleSets, 0)
}

// --- ValidateMatchmakingRuleSet ---

func TestE2E_ValidateMatchmakingRuleSet_Valid(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStack(t, false)
	client := newGameLiftClient(t, st.httpSrv.URL)

	out, err := client.ValidateMatchmakingRuleSet(context.Background(), &gamelift.ValidateMatchmakingRuleSetInput{
		RuleSetBody: aws.String(basicRuleSet),
	})
	require.NoError(t, err)
	assert.True(t, aws.ToBool(out.Valid))
}

func TestE2E_ValidateMatchmakingRuleSet_Invalid(t *testing.T) {
	if testing.Short() {
		t.Skip("e2e test (run without -short)")
	}
	st := buildStack(t, false)
	client := newGameLiftClient(t, st.httpSrv.URL)

	_, err := client.ValidateMatchmakingRuleSet(context.Background(), &gamelift.ValidateMatchmakingRuleSetInput{
		RuleSetBody: aws.String(`{}`),
	})
	require.Error(t, err)
}
