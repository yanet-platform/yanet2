# Generic filter

## Overview

Generic filter allows to find subset of predefined rules the packet satisfies to. 

For example, suppose the following rules are defined:
1. - Source port is in [10, 30]
   - Destination port is in [20, 30]
2. - Source port is in [25, 40]
   - Destination port is in [10, 35]

Then packet with source port equals to 15 and destination port equals to 25 satisfies to the first rule only. The packet with source port equals to 27 and destination port equals to 25 satisfies both rules.

Rules can be composed from restrictions on IP source/destination addresses, ports, VLAN and protocol types and flags. Also, rules are associated with actions. To be more precise, given packet, filter finds suitable rule with the smallest number and returns its action. If the action is not terminative, actions for the further suitable rules will be returned also, until the terminative action is found (**TODO** tests).

## Filter attributes

