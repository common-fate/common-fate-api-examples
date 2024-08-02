package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/common-fate/grab"
	"github.com/common-fate/sdk/config"
	accessv1alpha1 "github.com/common-fate/sdk/gen/commonfate/access/v1alpha1"
	directoryv1alpha1 "github.com/common-fate/sdk/gen/commonfate/control/directory/v1alpha1"
	entityv1alpha1 "github.com/common-fate/sdk/gen/commonfate/entity/v1alpha1"
	"github.com/common-fate/sdk/service/access"
	"github.com/common-fate/sdk/service/control/directory"

	"github.com/joho/godotenv"
)

var accountPtr = flag.String("account", "", "The AWS account to test access to")
var rolePtr = flag.String("role", "", "The role to test access to")
var userPtr = flag.String("user", "", "Email of the user to test access for")

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	flag.Parse()

	if *rolePtr == "" {
		err := errors.New("-role is required")
		log.Fatal(err)
	}

	if *accountPtr == "" {
		err := errors.New("-account is required")
		log.Fatal(err)
	}

	if *userPtr == "" {
		err := errors.New("-user is required")
		log.Fatal(err)
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

	user, err := findUserWithEmail(users, *userPtr)
	if err != nil {
		log.Fatal(err)
	}

	client := access.NewFromConfig(cfg)

	now := time.Now()

	result, err := client.DebugEntitlementAccess(ctx, connect.NewRequest(&accessv1alpha1.DebugEntitlementAccessRequest{
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
				Lookup: *accountPtr,
			},
		},
		Role: &accessv1alpha1.Specifier{
			Specify: &accessv1alpha1.Specifier_Lookup{
				Lookup: *rolePtr,
			},
		},
	}))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Can Request: %v\n", result.Msg.CanRequest)
	fmt.Printf("Is Auto Approved: %v\n", result.Msg.AutoApproved)

	fmt.Printf("Took: %v\n", time.Since(now))
}

func findUserWithEmail(users []*directoryv1alpha1.User, email string) (*directoryv1alpha1.User, error) {
	for _, u := range users {
		if u.Email == email {
			return u, nil
		}
	}

	return nil, fmt.Errorf("no user found with email %q", email)
}
