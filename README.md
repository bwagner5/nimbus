# nimbus

nimbus is a simple CLI tool to launch EC2 instances. 

## Examples

```
export VM_NAMESPACE=dev

> nimbus launch 

> nimbus list --env dev

> nimbus terminate 
```

## Launch Plans

```
> cat nimbus-launch-plan.yaml
apiVersion: github.com/bwagner5/nimbus/v1alpha1
kind: VirtualMachine
metadata:
    name: my-vm
    namespace: default
spec:
    instanceSpecs:
        instanceTypes: 
            - t4g.large
            - m5.large
        vcpus:
            min: 4
            max: 10
        memory:
            min: 4Gi
            max: 12Gi
        ...
    additionalDisks:
        -   name: data-disk-1
            size: 500Gi
            type: io2
    network:
        interfaces:
            -   name: eth0
                publicIP: true
                securityGroups:
                    selector:
                        tags:
                            key: environment
                            value: dev
    startupScript: |
        #!/bin/bash
        echo "Starting custom setup"
        apt-get update -y
        apt-get install -y nginx
    osImage:
        name: ubuntu-20.04
        version: latest
    lifecycle:
        spotInstance: false
        autoTerminateAfter: 24h
```

## Advanced

You can create custom VM configurations to distribute to make it super easy to start different types of VMs for your devs. 
The configurations include network information as well. Networks can be discovered or created based on tags. 

```
> cat my-company-vms.yaml
apiVersion: github.com/bwagner5/nimbus/v1alpha1
kind: VMAlias
metadata:
    name: dev-medium
    namespace: default
spec:
    instanceSpecs:
        instanceTypes: 
            - t4g.large
            - m5.large
        vcpus:
            min: 4
            max: 10
        memory:
            min: 4Gi
            max: 12Gi
        ...
    additionalDisks:
        -   name: data-disk-1
            size: 500Gi
            type: io2
    network:
        interfaces:
            -   name: eth0
                publicIP: true
                securityGroups:
                    selector:
                        tags:
                            key: environment
                            value: dev
    startupScript: |
        #!/bin/bash
        echo "Starting custom setup"
        apt-get update -y
        apt-get install -y nginx
    osImage:
        name: ubuntu-20.04
        version: latest
    lifecycle:
        spotInstance: false
        autoTerminateAfter: 24h
```

## Providers 

Providers can be implemented to perform the launch, list, and terminate actions. These providers can be used to provide a seamless experience across cloud providers or to output IaC files like Cloudformation or Terraform. 