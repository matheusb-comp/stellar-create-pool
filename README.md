## Installation

```
$ git clone https://github.com/matheusb-comp/stellar-create-pool.git
$ cd stellar-create-pool
```

Before compiling the source, though,
the `vendor` directory must include the dependencies.
And like [Stellar Go SDK](https://github.com/stellar/go#dependencies),
this repository uses [dep](https://golang.github.io/dep/).

To populate the `vendor` directory, install `dep` and inside the repository folder run:
```
$ dep ensure -v
```

Then to compile and install a `stellar-create-pool` executable to `$GOPATH/bin`, run:
```
$ go install .
```

## Usage

This tool is used to set the `inflation destination` of Stellar addresses.
Existing (_i.e._ funded) addresses can be provided in a [`JSON`](https://www.json.org/) file,
but the tool can also generate and fund new ones.
After it is done, all the addresses that have been successfully set are written to a file.

When running the tool, the options can be specified as flags, like `--help` or `-h`.

### Options

`-input <string>`:
Name of the JSON file (without extension) that holds a list of valid Stellar addresses.
Default: `accounts`.
Note that the file MUST follow the format of an array of objects with `pub` and `sec` attributes,
for example:
```
[
  {
    "pub": "GC64MQTOS5DGGRTMNTPJEMZ33QYQ2XAIDM7HP6W4JOLAWEBIFFJ22D5A",
    "sec": "SC6K4YGF6UXF5JSMXCXRNGBJ5JPDUUU7ADC434DCMFSBLMDYTD2CPZTN"
  },
  {
    "pub": "GA2CBRIOHVXYHHNDY2FX2M7ICXKCW2TPRT2MEZFHWWL76HJ7F5OEINNW",
    "sec": "SAI6RIKVKMCTCTKZNVXJ7OFHAROVSUDZG3NXQMUWYE4IMK7NDZE4QLSQ"
  }
]
```

`-output <string>`:
Name of the JSON file (without extension) that will have the list of successfull addresses.
Whenever the tool is run, any file with repeated name is substituted.
Default: `new_accounts`.

`-inflation <string>`:
Public key of the address that will be set as the `inflation destination` for all the accounts.

`-live`:
Run the tool on livenet.
By default it runs on testnet.

`-horizon <string>`:
URL of the [Horizon](https://github.com/stellar/go/tree/master/services/horizon) server to use.
For example, `http://localhost:8000`.
The default value for testnet is [`https://horizon-testnet.stellar.org`](https://horizon-testnet.stellar.org),
while for livenet it is [`https://horizon.stellar.org`](https://horizon.stellar.org).

`-num <int>`:
Number of new addresses to generate and fund.
Default: 10.

`-onlyGenerate`:
Stop after generating `num` addresses and writing the file,
do not fund or set any `inflation destination`.
Note that these accounts will not "exist" in the network.

`-sink`:
If running on testnet,
use the Stellar [friendbot](https://www.stellar.org/laboratory/#account-creator?network=test) to fund the addresses.

`-src <string>`:
Public key of the address that will fund the accounts, when not using `-sink`.

`-sec <string>`:
Secret seed of the address that will fund the accounts, when not using `-sink`.
Note that this string may be logged in [bash history files](https://www.gnu.org/software/bash/manual/html_node/Bash-History-Facilities.html),
so consider using [environment variables](http://tldp.org/LDP/Bash-Beginners-Guide/html/sect_03_02.html).

`-min <int>`:
Mininum value that will be transfered to fund the accounts, when not using `-sink` (in stroops).
Default: `40000000` (4 XLM).

`-max <int>`:
Maximum value that will be transfered to fund the accounts, when not using `-sink` (in stroops).
Default: `60000000` (6 XLM).

`-ops <int>`:
Number of operations that will be sent inside each transaction.
Default: 100 (max allowed: 100)
