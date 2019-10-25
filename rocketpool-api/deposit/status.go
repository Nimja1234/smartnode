package deposit

import (
    "github.com/urfave/cli"

    "github.com/rocket-pool/smartnode/shared/api/deposit"
    "github.com/rocket-pool/smartnode/shared/services"
    "github.com/rocket-pool/smartnode/shared/utils/api"
)


// Get the current deposit status
func getDepositStatus(c *cli.Context) error {

    // Initialise services
    p, err := services.NewProvider(c, services.ProviderOpts{
        CM: true,
        NodeContract: true,
        LoadContracts: []string{"rocketNodeAPI", "rocketNodeSettings"},
        LoadAbis: []string{"rocketNodeContract"},
        ClientConn: true,
        ClientSync: true,
        RocketStorage: true,
    })
    if err != nil { return err }
    defer p.Cleanup()

    // Get deposit status & print response
    if response, err := deposit.GetDepositStatus(p); err != nil {
        return err
    } else {
        api.PrintResponse(p.Output, response)
        return nil
    }

}

