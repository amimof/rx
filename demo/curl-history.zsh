# Fills the demo shell's history with curl calls to an OpenStack API behind
# a local auth proxy, for demo-curl.tape. Source it after `rx init zsh`.
#
# The history goes to a file under /tmp, never ~/.zsh_history. Timestamps
# are relative to now, so rx's recency ranking looks the same whenever the
# gif is rendered.
() {
  zmodload zsh/datetime
  HISTFILE=/tmp/rx-demo-curl.hist
  HISTSIZE=1000 SAVEHIST=1000
  setopt extended_history

  local api=https://localhost:7878

  # <minutes ago>|<command>, oldest first.
  local age cmd
  while IFS='|' read -r age cmd; do
    print -r -- ": $(( EPOCHSECONDS - age * 60 )):0;$cmd"
  done > $HISTFILE <<EOF
20100|curl -i -H "Content-Type: application/json" -d '{"auth": {"identity": {"methods": ["application_credential"]}}}' $api/identity/v3/auth/tokens -k
20090|curl -s $api/compute/v2.1/servers/detail -k | jq '.servers[].name'
20070|curl -s $api/compute/v2.1/flavors/detail -k
20050|curl -s $api/network/v2.0/networks -k
14500|curl -s -H "Accept: application/json" $api/image/v2/images -k
14480|curl -s $api/compute/v2.1/servers/detail --max-time 30 -k
14470|curl -s $api/volume/v3/volumes/detail -k
14400|curl -X POST -H "Content-Type: application/json" -d '{"server": {"name": "web-01", "flavorRef": "m1.small", "imageRef": "ubuntu-24.04"}}' $api/compute/v2.1/servers -k
10100|curl -s -H "OpenStack-API-Version: compute 2.79" $api/compute/v2.1/servers/detail -k
10090|curl -s $api/compute/v2.1/os-hypervisors/detail -k
10080|curl -s $api/network/v2.0/ports --max-time 10 -k
10060|curl -s $api/load-balancer/v2/lbaas/loadbalancers -k
10020|curl -X DELETE $api/compute/v2.1/servers/3f1c9a2e-7b4d-4e8a-9c61-0d5e2f8b7a14 -k
4400|curl -s $api/compute/v2.1/servers/detail --max-time 10 -k
4390|curl -s $api/compute/v2.1/flavors/detail --max-time 10 -k
4370|curl -s -o servers.json $api/compute/v2.1/servers/detail -k
4300|curl -X PUT -H "Content-Type: application/json" -d '{"server": {"name": "web-01-renamed"}}' $api/compute/v2.1/servers/3f1c9a2e-7b4d-4e8a-9c61-0d5e2f8b7a14 -k
1500|curl -s $api/image/v2/images --max-time 10 -k
1450|curl -s -H "Accept: application/json" $api/network/v2.0/networks -k
240|curl -i -H "Content-Type: application/json" -d '{"auth": {"identity": {"methods": ["application_credential"]}}}' $api/identity/v3/auth/tokens -k
230|curl -s $api/compute/v2.1/servers/detail --max-time 10 -k
200|curl -s $api/compute/v2.1/flavors/detail -k
120|curl -s $api/volume/v3/volumes/detail --max-time 30 -k
45|curl -s $api/compute/v2.1/servers/detail -k | jq '.servers[].status'
5|curl $api/compute/v2.1/servers/detail -k
EOF

  # Load it into the shell too, so Up recalls the last command.
  fc -R $HISTFILE
}
