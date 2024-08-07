package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"connectrpc.com/connect"
	"github.com/common-fate/grab"
	"github.com/common-fate/sdk/config"
	accessv1alpha1 "github.com/common-fate/sdk/gen/commonfate/access/v1alpha1"
	"github.com/common-fate/sdk/gen/commonfate/access/v1alpha1/accessv1alpha1connect"
	directoryv1alpha1 "github.com/common-fate/sdk/gen/commonfate/control/directory/v1alpha1"
	"github.com/common-fate/sdk/gen/commonfate/control/directory/v1alpha1/directoryv1alpha1connect"
	entityv1alpha1 "github.com/common-fate/sdk/gen/commonfate/entity/v1alpha1"
	"github.com/common-fate/sdk/service/access"
	"github.com/common-fate/sdk/service/control/directory"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

var filePtr = flag.String("file", "", "The YAML file defining the access tests")

func main() {
	_ = godotenv.Load()

	flag.Parse()

	if *filePtr == "" {
		err := errors.New("-file is required")
		log.Fatal(err)
	}

	testFileBytes, err := os.ReadFile(*filePtr)
	if err != nil {
		log.Fatal(fmt.Errorf("error reading tests file %q: %w", *filePtr, err))
	}

	var tests testFile

	err = yaml.Unmarshal(testFileBytes, &tests)
	if err != nil {
		log.Fatal(fmt.Errorf("error unmarshalling tests file %q (this usually means your file is incorrectly formatted or has invalid keys): %w", *filePtr, err))
	}

	ctx := context.Background()

	oidcClientID := os.Getenv("CF_OIDC_CLIENT_ID")
	oidcClientSecret := os.Getenv("CF_OIDC_CLIENT_SECRET")
	oidcURL := os.Getenv("CF_OIDC_ISSUER")
	apiURL := os.Getenv("CF_API_URL")

	cfg, err := config.New(ctx, config.Opts{
		APIURL:        apiURL,
		AccessURL:     apiURL,
		ClientID:      oidcClientID,
		ClientSecret:  oidcClientSecret,
		OIDCIssuer:    oidcURL,
		ConfigSources: []string{},
	})
	if err != nil {
		log.Fatal(err)
	}

	directoryClient := directory.NewFromConfig(cfg)

	fmt.Println("retrieving users for email address lookups...")

	users, err := grab.AllPages(ctx, func(ctx context.Context, nextToken *string) ([]*directoryv1alpha1.User, *string, error) {
		res, err := directoryClient.QueryUsers(ctx, connect.NewRequest(&directoryv1alpha1.QueryUsersRequest{
			PageToken: grab.Value(nextToken),
		}))
		if err != nil {
			return nil, nil, err
		}
		if res.Msg.NextPageToken != "" {
			return res.Msg.Users, &res.Msg.NextPageToken, nil
		}
		return res.Msg.Users, nil, nil
	})

	fmt.Printf("retrieved %v users\n", len(users))

	fmt.Println("\n\n-------------- ACCESS TESTS --------------")
	fmt.Printf("running %v access tests...\n", len(tests.AccessTests))

	accessClient := access.NewFromConfig(cfg)

	runner := TestRunner{
		AccessClient:    accessClient,
		DirectoryClient: directoryClient,
		Users:           users,
	}

	var failedAccessTests int

	for _, test := range tests.AccessTests {
		err = runner.RunAccessTest(ctx, test)
		if err != nil {
			fmt.Printf("[FAIL] %s %s to %s with role %s: %s\n", test.User, test.ExpectedResult, test.Target, test.Role, err.Error())
			failedAccessTests++
		} else {
			fmt.Printf("[PASS] %s %s to %s with role %s\n", test.User, test.ExpectedResult, test.Target, test.Role)
		}
	}

	fmt.Println("\n\n-------------- GROUP MEMBERSHIP TESTS --------------")
	fmt.Printf("running %v group membership tests...\n", len(tests.GroupTests))

	var failedMembershipTests int

	for _, test := range tests.GroupTests {
		memberText := "is not member of"

		if test.IsMember {
			memberText = "is member of"
		}

		err = runner.RunGroupMembershipTest(ctx, test)
		if err != nil {
			fmt.Printf("[FAIL] %s %s %s: %s\n", test.User, memberText, test.Group, err.Error())
			failedMembershipTests++
		} else {
			fmt.Printf("[PASS] %s %s %s\n", test.User, memberText, test.Group)
		}
	}

	if failedAccessTests > 0 {
		fmt.Printf("\n\n%v Access Tests failed\n", failedAccessTests)
		os.Exit(1)
	} else {
		fmt.Println("\n\nAll Access Tests passed")
	}

	if failedMembershipTests > 0 {
		fmt.Printf("\n%v Group Membership Tests failed\n", failedMembershipTests)
	} else {
		fmt.Println("\nAll Group Membership Tests passed")
	}

	if failedAccessTests > 0 || failedMembershipTests > 0 {
		os.Exit(1)
	}
}

type TestRunner struct {
	AccessClient    accessv1alpha1connect.AccessServiceClient
	DirectoryClient directoryv1alpha1connect.DirectoryServiceClient
	Users           []*directoryv1alpha1.User
}

func (r *TestRunner) RunGroupMembershipTest(ctx context.Context, test GroupTest) error {
	user, err := findUserWithEmail(r.Users, test.User)
	if err != nil && !test.IsMember {
		// don't fail if no-access and the user doesn't exist
		fmt.Printf("[WARN] error when finding user for email %s, ignoring because is-member is false: %s\n", test.User, err.Error())
		return nil
	}

	groupMemberships, err := grab.AllPages(ctx, func(ctx context.Context, nextToken *string) ([]*directoryv1alpha1.UserGroupMembership, *string, error) {
		res, err := r.DirectoryClient.QueryGroupsForUser(ctx, connect.NewRequest(&directoryv1alpha1.QueryGroupsForUserRequest{
			UserId:    user.Id,
			PageToken: grab.Value(nextToken),
		}))
		if err != nil {
			return nil, nil, err
		}
		if res.Msg.NextPageToken != "" {
			return res.Msg.Memberships, &res.Msg.NextPageToken, nil
		}
		return res.Msg.Memberships, nil, nil
	})

	var isMember bool
	for _, m := range groupMemberships {
		if m.Group.Id == test.Group {
			isMember = true
			break
		}
	}

	if test.IsMember && !isMember {
		return errors.New("user is not member of group")
	}
	if !test.IsMember && isMember {
		return errors.New("user is member of group")
	}

	return nil
}

func (r *TestRunner) RunAccessTest(ctx context.Context, test AccessTest) error {
	if test.ExpectedResult != "auto-approved" && test.ExpectedResult != "requires-approval" && test.ExpectedResult != "no-access" {
		return fmt.Errorf("invalid value for expected-result: %q - must be one of ['auto-approved', 'requires-approval', 'no-access']", test.ExpectedResult)
	}

	user, err := findUserWithEmail(r.Users, test.User)
	if err != nil && test.ExpectedResult == "no-access" {
		// don't fail if no-access and the user doesn't exist
		fmt.Printf("[WARN] error when finding user for email %s, ignoring because expected-result is no-access: %s\n", test.User, err.Error())
		return nil
	}

	if err != nil {
		return err
	}

	result, err := r.AccessClient.DebugEntitlementAccess(ctx, connect.NewRequest(&accessv1alpha1.DebugEntitlementAccessRequest{
		Principal: &accessv1alpha1.Specifier{
			Specify: &accessv1alpha1.Specifier_Eid{
				Eid: &entityv1alpha1.EID{
					Type: "CF::User",
					Id:   user.Id,
				},
			},
		},
		Target: &accessv1alpha1.Specifier{
			Specify: &accessv1alpha1.Specifier_Lookup{
				Lookup: test.Target,
			},
		},
		Role: &accessv1alpha1.Specifier{
			Specify: &accessv1alpha1.Specifier_Lookup{
				Lookup: test.Role,
			},
		},
	}))
	if err != nil {
		return fmt.Errorf("error calling the Common Fate DebugEntitlementAccess API: %w", err)
	}

	switch test.ExpectedResult {
	case "auto-approved":
		if result.Msg.CanRequest && result.Msg.AutoApproved {
			return nil
		}
		if result.Msg.CanRequest {
			return errors.New("got requires-approval")
		}
		return errors.New("got no-access")

	case "requires-approval":
		if result.Msg.CanRequest && result.Msg.AutoApproved {
			return errors.New("got auto-approved")
		}
		if result.Msg.CanRequest {
			return nil
		}
		return errors.New("got no-access")

	case "no-access":
		if result.Msg.CanRequest && result.Msg.AutoApproved {
			return errors.New("got auto-approved")
		}
		if result.Msg.CanRequest {
			return errors.New("got requires-approval")
		}
		return nil
	default:
		return fmt.Errorf("invalid expected-result value: %q", test.ExpectedResult)
	}
}

func findUserWithEmail(users []*directoryv1alpha1.User, email string) (*directoryv1alpha1.User, error) {
	for _, u := range users {
		if u.Email == email {
			return u, nil
		}
	}

	return nil, fmt.Errorf("no user found with email %q", email)
}

type testFile struct {
	AccessTests []AccessTest `yaml:"access-tests"`
	GroupTests  []GroupTest  `yaml:"group-tests"`
}

type AccessTest struct {
	User           string `yaml:"user"`
	Target         string `yaml:"target"`
	Role           string `yaml:"role"`
	ExpectedResult string `yaml:"expected-result"`
}

type GroupTest struct {
	User     string `yaml:"user"`
	Group    string `yaml:"group"`
	IsMember bool   `yaml:"is-member"`
}
