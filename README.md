# ddns-updater: Beget support fork

This is a fork of [ddns-updater](https://github.com/qdm12/ddns-updater), which adds [beget](https://beget.com) provider. This README contains only fork-specific information, read original README for more information.

## Usage
`beget_config.json.example` is a sample config. Set "login" and "password" to your Beget API credentials (IIRC, API password is different from your account password and is set separately), "domain" to the fully-qualified name of your domain and "priority" to your A's record priority (whatever this is), then move it to `data/config.json`. Speaking in [changeRecords](https://beget.com/en/kb/api/dns-administration-functions#changerecords) terms, "domain" is "fqdn", "priority" is "priority"; "login" and "password" parameters are "login" and "passwd" URL parameters.

Note: you can set only one A record. All other A records will be removed. Non-A records are preserved, but there is a race condition (updating record is done in two API calls: first current configuration is fetched, then it is send back with updated A record). 

## Testing tips
ddns-updater doesn't provide any testing environment (I may be wrong). You may create a temporary subdomain for working on this project and switch between two IPs on your development machine to make ddns-updater run its machinery. Setting `PERIOD` didn't decrease time between updates for me, so I sticked to deleting `data/updates.json` file and restarting ddns-updater to test changes.

## License
MIT
