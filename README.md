# About
This is the Console Operator Service.  It is responsible for managing the
Console Node pods.  It monitors the hardware on the system and scales 
the number of cray-console-node replicas to handle the number of consoles,
sets the number of consoles per cray-console-node pod, and moniters the
health of the Console Node pods.

## Related Software
*Console Data Service* handles recording the current states of which nodes 
are being monitored by which pods.

*Console Node Service* handles the direct console connections.  The number
of replicas is scaled to handle a reasonable number of connections per
node without running out of resources.

## Watching console log data
All of the console logs are available through the Console Operator service pod,
all Console Node pods, and through SMF.

To access the logs through the Console Operator service pod, find the pod name,
exec into the pod, then tail the correct console log file.  Each node has a
console log file in the directory /var/log/conman with the name console.XNAME
where XNAME is the full xname of the node.

```
ncn-m001: # kubectl -n services get pods | grep console-operator
cray-console-operator-677bc95cf9-wt8xt       2/2     Running     0          18h

ncn-m001: # kubectl -n services exec -it cray-console-operator-677bc95cf9-wt8xt -- sh
/ # tail -F /var/log/conman/console.XNAME
```

## Interactive access to a console connection
Each node has the console connection handled by one of the cray-console-node-N pods.  The
user must exec into the correct pod to connect to a particular node.  To find the correct
pod that is monitoring a particular node, query the Console Operator Service to find the
correct Console Node pod:
```
ncn-m001: # kubectl -n services exec cray-console-operator-677bc95cf9-wt8xt -- sh -c '/app/get-node XNAME'
{"podname":"cray-console-node-1"}
ncn-m001: # kubectl -n services exec -it cray-console-node-1 -- sh
sh-4.4# conman -j XNAME

<ConMan> Connection to console [XNAME] opened.

nid001722 login: 
```

## Versioning
Use [SemVer](http://semver.org/). The version is located in the [.version](.version) file. Other files either
read the version string from this file or have this version string written to them at build time using the 
[update_versions.sh](update_versions.sh) script, based on the information in the 
[update_versions.conf](update_versions.conf) file.

## Copyright and License
This project is copyrighted by Hewlett Packard Enterprise Development LP and is under the MIT
license. See the [LICENSE](LICENSE) file for details.

When making any modifications to a file that has a Cray/HPE copyright header, that header
must be updated to include the current year.

When creating any new files in this repo, if they contain source code, they must have
the HPE copyright and license text in their header, unless the file is covered under
someone else's copyright/license (in which case that should be in the header). For this
purpose, source code files include Dockerfiles, Ansible files, RPM spec files, and shell
scripts. It does **not** include Jenkinsfiles, OpenAPI/Swagger specs, or READMEs.

When in doubt, provided the file is not covered under someone else's copyright or license, then
it does not hurt to add ours to the header.