package main

import (
  "os"
  "fmt"
  "log"
  "flag"
  "sync"
  "time"
  "math"
  "strconv"
  "net/http"
  "math/rand"
  "io/ioutil"
  "encoding/json"
  "github.com/stellar/go/build"
  "github.com/stellar/go/keypair"
  "github.com/stellar/go/clients/horizon"
)

const WG_MAX = 25
const OPS_PER_TX_MAX = 100
const SIGNERS_PER_TX_MAX = 20
const TIMEOUT_WAIT_SECONDS = 5
const TESTNET_FRIENDBOT_URL = "https://friendbot.stellar.org/?addr="

type Voters []*keypair.Full

type TransactionCreator interface {
  // Builds a transaction with sequence seq and returns the base64 encoded XDR
  CreateTransaction(seq uint64, dest []*keypair.Full) (string, bool)
}
type AccountFunder struct {
  Min int
  Max int
  Pub string
  Sec string
  Seq uint64
}
type InflationSetter struct {
  C *horizon.Client
  InfDest string
}

type VoterJSON struct{
  Pub string `json:"pub"`
  Sec string `json:"sec"`
}
// type VotersJSON struct {
//   Pool   string      `json:"pool"`
//   Voters []VoterJSON `json:"voters"`
// }


var horizonURL, funderPub, funderSec, infDest, inputFile, outputFile string
var livenet, useSink, onlyGenerate bool
// TODO: minBal and maxBal should be uint64
var numAccounts, numOps, minBal, maxBal int

func init() {
  // Seed the pseudo-random generator
  rand.Seed(time.Now().UnixNano())
  // Set the flags default values and usage strings
  flag.StringVar(&horizonURL, "horizon", "",
    "URL of the Horizon server (default \"" +
      horizon.DefaultTestNetClient.URL +
      "\" for testnet and \"" +
      horizon.DefaultPublicNetClient.URL +
      "\" for livenet)",
  )
  flag.StringVar(&funderPub, "src",
    "GCFXD4OBX4TZ5GGBWIXLIJHTU2Z6OWVPYYU44QSKCCU7P2RGFOOHTEST",
    "Source address that will fund the accounts",
  )
  flag.StringVar(&funderSec, "sec", "",
    "Secret seed of the address being used to fund the accounts",
  )
  flag.StringVar(&infDest, "inflation", funderPub,
    "Address to set as the inflation destination in the accounts",
  )
  flag.StringVar(&inputFile, "input", "accounts",
    "Name of a JSON file with funded accounts to set the inflation. Format: " +
      "[ {\"pub\": <address:string>, \"sec\": <secret_seed:string>}, ... ]",
  )
  flag.StringVar(&outputFile, "output", "new_accounts",
    "Name of a JSON file to store the new accounts created, " +
      "truncating it if it already exists",
  )
  flag.IntVar(&numAccounts, "num", 10,
    "Number of accounts to create and fund",
  )
  flag.IntVar(&numOps, "ops", 100,
    "Number of operations to send in each transaction (max: " +
      strconv.Itoa(OPS_PER_TX_MAX) + ")",
  )
  flag.IntVar(&minBal, "min", 40000000,
    "Min value for the account random initial funding (in stroops)",
  )
  flag.IntVar(&maxBal, "max", 60000000,
    "Max value for the account random initial funding (in stroops)",
  )
  flag.BoolVar(&livenet, "live", false,
    "Create and fund the accounts on Stellar's livenet",
  )
  flag.BoolVar(&useSink, "sink", false,
    "Use Stellar's friendbot as the funder, if working on testnet",
  )
  flag.BoolVar(&onlyGenerate, "onlyGenerate", false,
    "Only generate new account keypairs, don't fund or set inflation",
  )
}

func validateFlags() {
  if numAccounts < 0 { numAccounts = 0 }
  if numOps < 1 { numOps = 1 }
  if numOps > OPS_PER_TX_MAX { numOps = OPS_PER_TX_MAX }
  if minBal < 10000000 { minBal = 10000000 }
  if maxBal < 10000001 { maxBal = 10000001 }
  if minBal == maxBal { maxBal = minBal + 1 }
  if maxBal < minBal {
    tmp := minBal
    minBal = maxBal
    maxBal = tmp
  }
  if funderSec != "" && (funderSec[0] != 'S' || len(funderSec) < 56) {
    log.Fatal("Error: Invalid secret key")
  }
  if funderSec == "" && !useSink && !onlyGenerate {
    log.Fatal("Error: Provide a secret key or " +
      "set a flag like 'sink' or 'onlyGenerate'")
  }
}

func main() {
  var wg sync.WaitGroup
  var client *horizon.Client

  // Parse and validate the command line arguments
  flag.Parse()
  validateFlags()

  // Set the Horizon client and URL
  if livenet {
    client = horizon.DefaultPublicNetClient
  } else {
    client = horizon.DefaultTestNetClient
  }
  if horizonURL != "" {
    client.URL = horizonURL
  }

  // Create the random Public-Secret keypairs
  pairs := make(Voters, numAccounts)
  for i, _ := range pairs {
    p, err := keypair.Random()
    fatalErr(err, "Error creating random keypair:")
    pairs[i] = p
    // fmt.Println(i, "-", p.Address(), p.Seed())
  }
  // Defer saving the keypairs in a file, only if the file name is not ""
  if outputFile != "" {
    defer saveJSON(&pairs)
  }
  // Stop here if we want only to generate random accounts
  if onlyGenerate {
    return
  }

  // ##### ACCOUNT FUNDING PROCESS #####

  // Set up the WaitGroup
  guard := make(chan struct{}, WG_MAX)
  // Set up the channel to store the indexes of successfully funded pairs
  successChan := make(chan int, len(pairs))

  // Fund all the accounts
  if !livenet && useSink {
    // Ask the friendbot to fund each pair, using goroutines
    for i, p := range pairs {
      // Use an empty struct to mark that a new goroutine will be used
      // This blocks when guard is full
      guard<- struct{}{}
      wg.Add(1)
      // Start the goroutine
      go func(i int, p *keypair.Full, success chan int) {
        defer wg.Done()
        fmt.Println("Ask friendbot to fund #", i, "-", p.Address()) // TODO: Remove
        // Returns true if the friendbot successfully funded p
        if askFriendBot(p) {
          success<- i
        }
        // Remove one element from the guard, allowing a new goroutine to run
        <-guard
      }(i, p, successChan)
    }

    // Wait for all the goroutines to finish
    wg.Wait()
    // Create a new slice to hold only the funded accounts
    var tmp Voters
    // Proccess the results
    sinkProcess:
    for {
      select {
      case i := <-successChan:
        fmt.Println("- Keeping pair #", i, "-", fmt.Sprintf("%p", pairs[i]))
        tmp = append(tmp, pairs[i])
        break
      default:
        fmt.Println("- successChan is empty. Done!")
        break sinkProcess
      }
    }
    // Keep only the funded accounts in the pairs slice (and update numAccounts)
    pairs = tmp
    numAccounts = len(pairs)
  } else {

    // TODO: Testing...
    funder := AccountFunder{
      Min: minBal,
      Max: maxBal,
      Pub: funderPub,
      Sec: funderSec,
    }
    creator := TransactionCreator(funder)

    // Fund the accounts from funderPub's balance, numOps per transaction
    var succeeded Voters
    for processed := 0; processed < len(pairs); {
      // Indexes of the pairs that will be funded
      a := processed
      b := processed + numOps
      // Make sure we don't overflow
      if b > len(pairs) {
        b = len(pairs)
      }
      fmt.Println("\nProcess from #", a, "to #", b-1)

      // Get the Sequence number for the funder account
      fmt.Println("Getting funder sequence number from horizon...")
      sequence, err := getSequence(client, funderPub)
      fatalErr(err, "Error getting funder's sequence from Horizon:")
      fmt.Println("Sequence:", sequence - 1)

      succeeded = append(succeeded, createAndSubmit(client, &creator, sequence, pairs[a:b])...)
      fmt.Println("### SUCCEEDED:", len(succeeded))

      // We have processed up to 'b' already
      processed = b
    }

    // TODO: Testing...
    pairs = succeeded
    numAccounts = len(pairs)
  }

  // Read extra (funded) addresses from a file, only if its name is not ""
  if inputFile != "" {
    inputPairs := readJSON()
    if inputPairs != nil {
      pairs = append(pairs, (*inputPairs)...)
      numAccounts += len(*inputPairs)
    }
  }

  // ##### INFLATION DESTINATION SETTING PROCESS #####

  fmt.Println("\n### Set inflation destination\n")
  ceil := math.Ceil(float64(numAccounts) / float64(SIGNERS_PER_TX_MAX))
  // respChan := make(chan Response, int(ceil))
  respChan := make(chan Voters, int(ceil))

  // TODO: Testing...
  inf := InflationSetter{
    C: client,
    InfDest: infDest,
  }
  creator := TransactionCreator(inf)

  // Set their Inflation Destination to the funder, 20 per transaction
  for a := 0; a < numAccounts; a += SIGNERS_PER_TX_MAX {
    // Indexes of the pairs that will have operations in the transaction
    b := a + SIGNERS_PER_TX_MAX
    // Make sure we don't overflow
    if b > numAccounts {
      b = numAccounts
    }
    fmt.Println("Setting from #", a, "to #", b-1)

    // Use an empty struct to mark that a new goroutine will be used
    // This blocks when guard is full
    guard<- struct{}{}
    wg.Add(1)
    // Start the goroutine
    go func(a int, b int, resp chan Voters) {
      defer wg.Done()

      resp<- createAndSubmit(client, &creator, 0, pairs[a:b])
      <-guard
    }(a, b, respChan)
  }

  // Wait for all the goroutines to finish
  wg.Wait()
  fmt.Println("\nAll goroutines done! Proccessing results...")
  // Proccess the results
  var succeeded Voters
  process:
  for {
    select {
    case r := <-respChan:
      succeeded = append(succeeded, r...)
      fmt.Println("### SUCCEEDED:", len(r))
      break
    default:
      fmt.Println("- respChan is empty. Done!")
      break process
    }
  }

  pairs = succeeded
  fmt.Println("### Final succeeded:", len(pairs))
}

// TODO: It should also return res (type *horizon.TransactionSuccess)
func createAndSubmit(c *horizon.Client, src *TransactionCreator, seq uint64, pairs Voters) (Voters) {
  // Create and submit the transaction (retry if some operations fail)
  for count := 1; ; count, seq = count + 1, seq + 1 {
    // Get the signed Transaction Envelope
    xdr, notOk := (*src).CreateTransaction(seq, pairs)
    // Failed to create the transaction, no pair succeeded, stop trying
    if notOk {return Voters{}}

    // Submit the transaction
    res, err := submit(c, xdr)
    if logErr(err, "CreateAccount submission error (try #" + strconv.Itoa(count) + "):") {
      // Log the XDR of the failed transaction
      log.Println("XDR of the failed transaction:", xdr)
      // Log the specific Horizon errors and get the Transaction Codes
      codes, notOk := checkHorizonError(err)
      // The error is not from horizon, or it didn't fail because of the operations
      if notOk || codes.TransactionCode != "tx_failed" {
        return Voters{}
      }

      // Make pairs point to a new slice, with only the successfull elements
      var tmp Voters
      for i, c := range codes.OperationCodes {
        if c == "op_success" {
          tmp = append(tmp, pairs[i])
        }
      }
      // Try again with the updated pairs
      pairs = tmp
    } else {
      // Transaction was successfull (with maybe less voters in pairsCopy)
      fmt.Println("Transaction Sent! Number of pairs:", len(pairs))
      fmt.Println("\tLedger:", res.Ledger)
      fmt.Println("\tHash:", res.Hash)
      // fmt.Println("\tResult:", res.Result)

      // Transaction submission was successfull, get out of loop
      break
    }
  }

  // Return whatever pairs remain (the ones that succeeded)
  return pairs
}


func (m AccountFunder) CreateTransaction(seq uint64, dest []*keypair.Full) (string, bool) {
  // Create a mutator for each createAccount operation
  muts := make([]build.TransactionMutator, len(dest))
  for i, p := range dest {
    // Calculate the initial balance for the operation
    r := float64(m.Min + rand.Intn(m.Max - m.Min)) / 10000000.0
    amount := strconv.FormatFloat(r, 'f', -1, 64)
    // fmt.Println("Adding OP - Create", p.Address(), "With", amount, "XLM")

    // Add the operation to the slice
    muts[i] = build.CreateAccount(
      build.Destination{ p.Address() },
      build.NativeAmount{ amount },
    )

    // TODO: Remove - just testing operation failures
    // x := rand.Intn(100)
    // if x < 2 {
    //   fmt.Println("@@@@ Introducing invalid @@@@", x)
    //   muts[i] = build.CreateAccount(
    //     build.Destination{ "GCBOIVJS44UAVOBLUHR6EXHOKPPNZXDP3IMOZOQ7LKGX3ZZX2SOHJB7B" }, //INVALID!
    //     build.NativeAmount{ amount },
    //   )
    // } else {
    //   muts[i] = build.CreateAccount(
    //     build.Destination{ p.Address() },
    //     build.NativeAmount{ amount },
    //   )
    // }
    // TODO: End-Remove
  }

  // Create the transaction with these mutators and get the XDR
  tx, notOk := createTx(m.Pub, seq, m.Pub[len(m.Pub)-8:] + " funding accounts", []string{m.Sec}, muts...)
  if notOk {
    return "", true
  } else {
    return tx, false
  }
}


func (m InflationSetter) CreateTransaction(seq uint64, dest []*keypair.Full) (string, bool) {
  // There must be at least one keypair to create the transaction
  if len(dest) <= 0 { return "", true }

  // Get the sequence number of the first pair (ignore the 'seq' received)
  pub := dest[0].Address()
  seq, err := getSequence(m.C, pub)
  if logErr(err, "Error getting sequence from Horizon:") {return "", true}
  fmt.Println(pub, "sequence:", seq)

  // Create a mutator for each setOptions operation
  muts := make([]build.TransactionMutator, len(dest))
  signers := make([]string, len(dest))
  for i, p := range dest {
    muts[i] = build.SetOptions(
      build.SourceAccount{ p.Address() },
      build.InflationDest(m.InfDest),
    )
    // Also save this pair secret key as a signer
    signers[i] = p.Seed()
  }

  // Create the transaction with these mutators and get the XDR
  tx, notOk := createTx(pub, seq, "Voting for " + m.InfDest[len(m.InfDest)-8:], signers, muts...)
  if notOk {
    return "", true
  } else {
    return tx, false
  }
}

// General function to create transactions, checking each step along the way
func createTx(src string, seq uint64, memo string, signers []string, muts ...build.TransactionMutator) (string, bool) {
  // fmt.Println("Creating TX - src:", src, "- seq:", seq)
  // Choose the network passphrase
  var network build.Network
  if livenet {
    network = build.PublicNetwork
  } else {
    network = build.TestNetwork
  }

  // Create the base transaction
  tx, err := build.Transaction(
    build.SourceAccount{ src },
    build.Sequence{ seq },
    build.MemoText{ memo },
    network,
  )
  if logErr(err, "Error building base transaction:") {return "", true}

  // Set the operations (received as a slice of TransactionMutators)
  err = tx.Mutate(muts...)
  if logErr(err, "Error mutating transaction:") {return "", true}

  // Run the default mutations, such as calculating the Fee
  err = tx.Mutate(build.Defaults{})
  if logErr(err, "Error applying default mutations:") {return "", true}

  // Sign the transaction with the slice of private keys received
  txe, err := tx.Sign(signers...)
  if logErr(err, "Error signing the transaction:") {return "", true}

  // Get the XDR in Base64 from the Transaction Envelope
  txb64, err := txe.Base64()
  if logErr(err, "Error getting XDR from the Tx envelope:") {return "", true}

  // The transaction is finally done!
  return txb64, false
}

func submit(client *horizon.Client, xdr string) (*horizon.TransactionSuccess, error) {
  var err error
  var res horizon.TransactionSuccess
  // Try susbmitting the transaction
  for retry, count := true, 1; retry; count++ {
    res, err = client.SubmitTransaction(xdr)
    // Type assertion to test if err is from Horizon (herr is nil if err is nil)
    herr, isHorizonErr := err.(*horizon.Error)
    // Wait some time and retry, if we got Status 504 (Gateway Timeout
    if isHorizonErr && herr.Problem.Status == 504 {
      log.Println("Horizon timed out:", herr.Problem.Type)
      time.Sleep(TIMEOUT_WAIT_SECONDS * time.Second)
      log.Println("Re-submitting...")
      continue
    }
    // Do not retry, err == nil or it was not a timeout
    retry = false
  }
  return &res, err
}

func getSequence(client *horizon.Client, address string) (uint64, error) {
  // Get the Sequence number for an account
  // Returned type: xdr.SequenceNumber -> xdr.Int64 -> int64
  seq, err := client.SequenceForAccount(address)
  if err != nil || seq < 0 {
    return 0, err
  }
  sequence := uint64(seq) + 1
  return sequence, nil
}

func askFriendBot(p *keypair.Full) bool {
  resp, err := http.Get(TESTNET_FRIENDBOT_URL + p.Address())
  if logErr(err, "Error funding account with the friendbot:") {
    return false
  }
  defer resp.Body.Close()

  fmt.Println("Pair:", fmt.Sprintf("%p", p), "- Addr:", p.Address(), "- Response:", resp.StatusCode, "-", resp.Status) // TODO: Remove
  // Maybe the 'if' should test for StatusCode != 200 ?
  if resp.StatusCode < 200 || resp.StatusCode > 299 {
    body, err := ioutil.ReadAll(resp.Body)
    if !logErr(err, "Error reading the body of the friendbot's response:") {
      log.Println("Status", resp.Status, "funding", p.Address(), "on FriendBot")
      log.Println(string(body))
    }
    return false
  }
  // This means the friendbot returned any status code between 200 and 299 (success)
  return true
}

func readJSON() *Voters {
  fmt.Println("\nReading", inputFile, "...")
  // Open the JSON file
  f, err := os.Open(inputFile + ".json")
  if logErr(err, "Error opening " + inputFile + ".json:") { return nil }
  defer f.Close()

  // Create the keypairs slice to append the data
  var keypairs Voters

  // Create a JSON decoder and Unmarshall the file
  dec := json.NewDecoder(f)
  // Start the array by reading an open bracket ('[')
  t, err := dec.Token()
  if logErr(err, "Error getting token '[' from file:") || t != json.Delim('[') {
    log.Println("Decoding JSON file: Wrong token. Expected '[', got:", t)
    return nil
  }
  // While the array contain JSON values
  for dec.More() {
    // Decode the voter
    var v VoterJSON
    err = dec.Decode(&v)
    if logErr(err, "Error decoding voter:") { return nil }

    // Get a keypair from the decoded secret seed
    kp, err := keypair.Parse(v.Sec)
    if !logErr(err, "Error parsing keypair from decoded voter:") {
      // Assert type
      pointer, ok := kp.(*keypair.Full)
      if ok {
        // Append keypair to slice
        keypairs = append(keypairs, pointer)
      }
    }
  }
  // Finish the array by reading a closing bracket (']')
  t, err = dec.Token()
  if logErr(err, "Error getting token ']' from file:") || t != json.Delim(']') {
    log.Println("Decoding JSON file: Wrong token. Expected ']', got:", t)
    return nil
  }

  return &keypairs
}

func saveJSON(pairsPointer *Voters) {
  fmt.Println("\nSaving", pairsPointer, "...")
  // Iterate all the voters and prepare the JSON struct
  // var jsonStruct VotersJSON
  // jsonStruct.Pool = infDest
  var jsonVoters []VoterJSON
  for _, p := range *pairsPointer {
    // jsonStruct.Voters = append(jsonStruct.Voters, VoterJSON
    jsonVoters = append(jsonVoters, VoterJSON{
      Pub: p.Address(),
      Sec: p.Seed(),
    })
  }

  // Create/truncate the file to save the keypairs (if error, dump data on logs)
  f, err := os.Create(outputFile + ".json")
  if logDumpData(err, jsonVoters, "Error creating " + outputFile + ".json:") {
    return
  }
  defer f.Close()

  // Create a JSON encoder with the file and Marshal the structure
  enc := json.NewEncoder(f)
  enc.SetIndent("", " ")
  err = enc.Encode(jsonVoters)
  // In case of errors, try to save the data in Go's format (no JSON)
  if logErr(err, "Error encoding JSON: ") {
    log.Println("Trying to save data in Go's format...")
    _, err = fmt.Fprintf(f, "%#v", jsonVoters)
    // If it still errors, just dump the data in the log
    logDumpData(err, jsonVoters, "Error saving data to the file:")
  }
}

func logErr(err error, message string) bool {
  if err != nil {
    log.Println(message, err)
    return true
  } else {
    return false
  }
}

func checkHorizonError(err error) (*horizon.TransactionResultCodes, bool) {
  // Type assertion to test if err is from Horizon (herr is nil if err is nil)
  herr, isHorizonErr := err.(*horizon.Error)
  if !isHorizonErr {return nil, true}

  // Log the Horizon Error Status
  log.Println("\tError Status:", herr.Problem.Status, herr.Problem.Type)

  // Log the Transaction Result String (base64)
  str, err := herr.ResultString()
  if !logErr(err, "\tError extracting the result string from the Horizon Error:") {
    log.Println("\tTransaction result XDR:", str)
  }

  // Get the Transaction Result Codes
  codes, err := herr.ResultCodes()
  if logErr(err, "\tError extracting the result codes from the Horizon Error:") {
    return nil, true
  }

  // Log and return the Transaction Result Codes
  log.Println("\tTransaction result code:", codes.TransactionCode, "(" + strconv.Itoa(len(codes.OperationCodes)) + " op_codes)")
  for i, c := range codes.OperationCodes {
    log.Println("\t\tOperation", i, "result code:", c)
  }
  return codes, false
}

func logDumpData(err error, data interface{}, message string) bool {
  if logErr(err, message) {
    log.Printf("## DATA DUMP ##\n%#v\n", data)
    return true
  } else {
    return false
  }
}

func fatalErr(err error, message string) {
  if logErr(err, message) {
    os.Exit(1)
  }
}
