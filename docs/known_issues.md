# Known issues

## Gateway is not working after restart

**First Reported:**
2024-07-19

**Problem:**
Gateway keeps on restarting on bootup. It happened after the machine was restarted. The gateway has not been updated and still it is crashing.

**Root Cause:**
The version of Gateway that was installed prior to July 18 2024, had hard coded a short list of standard registries. This included a registry which has been decomissioned in August. The result of this is that the gateway tries to contact this decomissioned registry during bootup and since it cannot find it, crashes and restarts. This happens with older gateways even without updating the software. If the old gateway software was restarted due to whatever reason, this issue starts to show up.

**Solution:**
Update the gateway software with the new image (built after July 18 2024). This has removed the hard code of the decomissioned registry and everything should work ok. The following is one way of updating the gateway software. Perform these on the machine that has the gateway container within the beckn-onix/install folder after updating the beckn-onix git clone.

```
$ docker-compose -f docker-compose-gateway.yml down
$ docker rmi fidedocker/gateway
$ docker-compose -f docker-compose-gateway.yml up --detach
```
