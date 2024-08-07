Examples of using the Common Fate API:

## Set up

To run this locally, follow the instructions below:

1. Copy `.env.example` to `.env`.
2. Add your Common Fate environment variables to `.env`. You'll need to use the Read Only client ID and client secret for the example.

## Entitlement access API

Run the example:

```bash
go run main.go -role BreakGlass -account ExampleAccount -user example-user@example.com
```

## Test group membership API

Run the example:

```bash
go run main.go -user example-user@example.com -group-id GroupID
```
