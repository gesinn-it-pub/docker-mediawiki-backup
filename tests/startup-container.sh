#!/bin/bash

set -euxo pipefail

initialize-wiki.sh
apache2-foreground
