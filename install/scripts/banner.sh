#!/bin/bash
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
source $SCRIPT_DIR/variables.sh

# Define the text to print in the banner
text="
  ######    #######    #####    #    #    #     #
  #     #   #         #     #   #   #     ##    #
  #     #   #         #         #  #      # #   #
  ######    #####     #         ###       #  #  #
  #     #   #         #         #  #      #   # #
  #     #   #         #     #   #   #     #    ##
  ######    #######    #####    #    #    #     #
"

text2="
 ########   ########   ######   ##    ##  ##    ## 
 ##     ##  ##        ##    ##  ##   ##   ###   ## 
 ##     ##  ##        ##        ##  ##    ####  ## 
 ########   ######    ##        #####     ## ## ## 
 ##     ##  ##        ##        ##  ##    ##  #### 
 ##     ##  ##        ##    ##  ##   ##   ##   ### 
 ########   ########   ######   ##    ##  ##    ## 
"
# Clear the terminal screen
clear
echo "${GREEN}$text2${NC}"
