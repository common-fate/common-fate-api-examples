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
	directoryv1alpha1 "github.com/common-fate/sdk/gen/commonfate/control/directory/v1alpha1"
	"github.com/common-fate/sdk/service/control/directory"

	"github.com/joho/godotenv"
)

var groupIDPtr = flag.String("group-id", "", "The Group ID to test membership of")
var userPtr = flag.String("user", "", "Email of the user to test group membership of")

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	flag.Parse()

	if *groupIDPtr == "" {
		err := errors.New("-group-id is required")
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

	client := directory.NewFromConfig(cfg)

	users, err := grab.AllPages(ctx, func(ctx context.Context, nextToken *string) ([]*directoryv1alpha1.User, *string, error) {
		res, err := client.QueryUsers(ctx, connect.NewRequest(&directoryv1alpha1.QueryUsersRequest{
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

	groupMemberships, err := grab.AllPages(ctx, func(ctx context.Context, nextToken *string) ([]*directoryv1alpha1.UserGroupMembership, *string, error) {
		res, err := client.QueryGroupsForUser(ctx, connect.NewRequest(&directoryv1alpha1.QueryGroupsForUserRequest{
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

	for _, m := range groupMemberships {
		if m.Group.Id == *groupIDPtr {
			fmt.Printf("user %s (email %s) is a member of group %s (%s)\n", user.Id, user.Email, m.Group.Id, m.Group.Name)
			os.Exit(0)
		}
	}

	// if we get here, the user is not a member of the group.
	log.Fatalf("user %s (email %s) is not a member of group %s\n", user.Id, user.Email, *groupIDPtr)
}

func findUserWithEmail(users []*directoryv1alpha1.User, email string) (*directoryv1alpha1.User, error) {
	for _, u := range users {
		if u.Email == email {
			return u, nil
		}
	}

	return nil, fmt.Errorf("no user found with email %q", email)
}
