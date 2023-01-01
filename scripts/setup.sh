make all

rm -rf ~/.hdc

mkdir ~/.hdc

hdc init --chain-id test test
hdc keys add cooluser --keyring-backend test
hdc add-genesis-account cooluser 100000000000000stake,10000000000000000uhdc --keyring-backend test
hdc gentx cooluser 1000000000stake --chain-id test --keyring-backend test
hdc collect-gentxs
hdc start