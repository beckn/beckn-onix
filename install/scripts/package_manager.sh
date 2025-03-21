#!/bin/bash
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
source $SCRIPT_DIR/variables.sh

#Required packages list as below.
package_list=("docker" "docker-compose" "jq")

command_exists() {
  command -v "$1"  &>/dev/null
}

# Redirect input from /dev/null to silence prompts
export DEBIAN_FRONTEND=noninteractive
export APT_KEY_DONT_WARN_ON_DANGEROUS_USAGE=1


#Install Package
install_package() {
    if [ -x "$(command -v apt-get)" ]; then
        # APT (Debian/Ubuntu)
        if [ "$1" == "docker" ]; then
            if ! docker --version > /dev/null 2>&1; then
                if [ "$(lsb_release -is | tr -d '[:space:]' | tr '[:upper:]' '[:lower:]')" = "ubuntu" ]; then
                    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg
                    echo "deb [signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
                else
                    curl -fsSL https://download.docker.com/linux/debian/gpg | sudo gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg
                    echo "deb [signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/debian $(lsb_release -cs) stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
                fi
                sudo apt update >/dev/null 2>&1
                sudo apt install -y docker-ce docker-ce-cli containerd.io >/dev/null 2>&1
                sudo usermod -aG docker $USER
                source ~/.bashrc
                sudo systemctl enable docker.service
                sudo systemctl restart docker.service
            else
                echo "Docker is already installed."
            fi
        else
            if ! dpkg -l | grep -q "^ii  $1 "; then
                sudo apt-get update >/dev/null 2>&1
                sudo apt-get install -y $1 >/dev/null 2>&1
            else
                echo "$1 is already installed."
            fi
        fi
    elif [ -x "$(command -v yum)" ]; then
        # YUM (Red Hat/CentOS/Amazon Linux)
        if [ "$1" == "docker" ]; then
            if ! docker --version > /dev/null 2>&1; then
                if [ -f /etc/centos-release ]; then
                    sudo yum-config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
                elif [ -f /etc/redhat-release ]; then
                    sudo yum-config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
                elif grep -q "Amazon Linux release 2" /etc/system-release; then
                    sudo amazon-linux-extras install docker -y
                elif grep -q "Amazon Linux release" /etc/system-release; then
                    sudo yum install -y docker
                fi
                sudo yum install -y docker-ce docker-ce-cli containerd.io >/dev/null 2>&1
                sudo usermod -aG docker $USER
                source ~/.bashrc
                sudo systemctl enable docker.service
                sudo systemctl restart docker.service
            else
                echo "Docker is already installed."
            fi
        else
            if ! rpm -qa | grep -q "^$1-"; then
                sudo yum install -y $1 >/dev/null 2>&1
            else
                echo "$1 is already installed."
            fi
        fi
    elif [ -x "$(command -v amazon-linux-extras)" ]; then
        # Amazon Linux 2 specific
        if [ "$1" == "docker" ]; then
            if ! docker --version > /dev/null 2>&1; then
                sudo amazon-linux-extras install docker -y >/dev/null 2>&1
                sudo systemctl enable docker.service
                sudo systemctl start docker.service
                sudo usermod -aG docker $USER
                source ~/.bashrc
            else
                echo "Docker is already installed."
            fi
        else
            if ! amazon-linux-extras list | grep -q "$1"; then
                sudo amazon-linux-extras install $1 -y >/dev/null 2>&1
            else
                echo "$1 is already installed."
            fi
        fi
    else
        echo "Unsupported package manager. Please install $1 manually."
        exit 1
    fi
}



remove_package(){
    if [ -x "$(command -v apt-get)" ]; then
        # APT (Debian/Ubuntu)
        sudo apt-get purge -y $1  >/dev/null 2>&1
        sudo apt autoremove -y  >/dev/null 2>&1
    elif [ -x "$(command -v yum)" ]; then
        # YUM (Red Hat/CentOS)
        sudo yum remove -y $1  >/dev/null 2>&1
        sudo yum autoremove -y >/dev/null 2>&1
    fi
}

# Function to install Docker
install_docker_bash() {
    # Install Docker Bash completion
    echo "Installing Docker Bash completion..."
    sudo curl -L https://raw.githubusercontent.com/docker/cli/master/contrib/completion/bash/docker -o /etc/bash_completion.d/docker
}

# Function to install Docker Compose
install_docker_compose() {
    command_exists docker-compose
    if [ $? -eq 0 ]; then
        echo "docker-compose is already installed."
        return
    else
        echo "Installing Docker Compose..."
        sudo curl -L "https://github.com/docker/compose/releases/latest/download/docker-compose-$(uname -s)-$(uname -m)" -o /usr/local/bin/docker-compose
        sudo chmod +x /usr/local/bin/docker-compose
    fi

    # Check if Docker Compose installation was successful
    if [ $? -eq 0 ]; then
        echo "Docker Compose installed successfully."
    else
        echo "${RED}Failed to install Docker Compose. Exiting.${NC}"
        exit 1
    fi

    if [ -f /etc/bash_completion.d/docker-compose ]; then
        echo "Docker Compose Bash completion is already installed."
    else
        # Install Docker Compose Bash completion
        echo "Installing Docker Compose Bash completion..."
        sudo curl -L https://raw.githubusercontent.com/docker/compose/master/contrib/completion/bash/docker-compose -o /etc/bash_completion.d/docker-compose
    fi
}


# Check if package is already installed

for package in "${package_list[@]}"; do
    if ! command_exists $package; then
	install_package "$package"
    fi
    if [ "$package" == "docker" ]; then
        if [[ $(uname -s ) == 'Linux' ]];then        
            if [ -f /etc/bash_completion.d/docker ]; then
                echo "Docker Bash completion is already installed."
            else
                install_docker_bash
            fi
        fi
    fi
    if [ "$package" == "docker-compose" ]; then
        if [[ $(uname -s ) == 'Linux' ]];then
            install_docker_compose
        fi
    fi
done

