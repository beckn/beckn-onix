# Nginx sample configuration for the different components

This document lists the various Nginx configuration sample files used in the demo. These use the URLs used as example in the user guide and demo walkthrough. These can be used as a reference.

## Nginx sample configuration for Registry

Here is a sample Nginx configuration file for the registry. It uses the 'https://onix-registry.becnkprotocol.io' as the example Registry URL.

```
server {
    listen 80;
    listen [::]:80;
    server_name onix-registry.becknprotocol.io;

    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
  underscores_in_headers on;
  gzip on;
        gzip_disable "msie6";
        gzip_vary on;
        gzip_proxied any;
        gzip_comp_level 6;
        gzip_buffers 16 8k;
        gzip_http_version 1.1;
        gzip_min_length 256;
        gzip_types text/plain text/css application/json application/x-javascript text/xml application/xml application/xml+rss application/javascript text/javascript application/vnd.ms-fontobject application/x-font-ttf font/opentype image/svg+xml image/x-icon font/woff font/woff2 application/octet-stream font/ttf ;

    server_name onix-registry.becknprotocol.io;

    ssl_certificate /etc/letsencrypt/live/onix-registry.becknprotocol.io/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/onix-registry.becknprotocol.io/privkey.pem;
    #include /etc/letsencrypt/options-ssl-nginx.conf;
    #ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

    access_log /var/log/nginx/app_beckn_registry_access.log;
    error_log /var/log/nginx/app_beckn_registry_error.log debug;
    client_max_body_size 10M;

    location / {
        if ($uri ~* "\.(jpg|jpeg|png|gif|ico|ttf|eot|svg|woff|woff2|css|js)$") {
            add_header 'Cache-Control' 'no-cache';
        }

        #aio threads=default;

        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

	#proxy_http_version 1.1;
	proxy_set_header X-URIScheme https;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";

        proxy_pass "http://localhost:3030";


        set $cors 'true';

#        if ($http_origin ~ '^https?://(localhost|registry-energy\.becknprotocol\.io)$') {
#            set $cors 'true';
#        }
#
        add_header 'Access-Control-Allow-Origin' "$http_origin" always;

        if ($cors = 'true') {
            add_header 'Access-Control-Allow-Credentials' 'true' always;
            add_header 'Access-Control-Allow-Methods' 'GET, POST, PUT, DELETE, OPTIONS' always;
            add_header 'Access-Control-Allow-Headers' 'Accept,Authorization,Cache-Control,Content-Type,DNT,If-Modified-Since,Keep-Alive,Origin,User-Agent,X-Requested-With,Range,ApiKey,pub_key_format' always;
        }

        if ($request_method = 'OPTIONS') {
            add_header 'Access-Control-Allow-Origin' "$http_origin" always;
            add_header 'Access-Control-Allow-Credentials' 'true' always;
            add_header 'Access-Control-Allow-Methods' 'GET, POST, PUT, DELETE, OPTIONS' always;
            add_header 'Access-Control-Allow-Headers' 'Accept,Authorization,Cache-Control,Content-Type,DNT,If-Modified-Since,Keep-Alive,Origin,User-Agent,X-Requested-With,Range,ApiKey,pub_key_format' always;
            add_header 'Access-Control-Max-Age' 1728000;
            add_header 'Content-Type' 'text/plain charset=UTF-8';
            add_header 'Content-Length' 0;
            return 204;
        }
    }
}
```

## Nginx sample configuration for Gateway

Here is a sample Nginx configuration for the gateway. It uses the 'https://onix-gateway.becknprotocol.io' as the example Gateway URL.

```
server {
          server_name onix-gateway.becknprotocol.io;

    gzip on;
        gzip_disable "msie6";
        gzip_vary on;
        gzip_proxied any;
        gzip_comp_level 6;
        gzip_buffers 16 8k;
        gzip_http_version 1.1;
        gzip_min_length 256;
        gzip_types text/plain text/css application/json application/x-javascript text/xml application/xml application/xml+rss application/javascript text/javascript application/vnd.ms-fontobject application/x-font-ttf font/opentype image/svg+xml image/x-icon font/woff font/woff2 application/octet-stream font/ttf ;



    access_log /var/log/nginx/app_beckn_gateway_access.log;
    error_log /var/log/nginx/app_beckn_gateway_error.log;
    client_max_body_size 10M;

    ### ssl config - customize as per your setup ###
keepalive_timeout    70;
ignore_invalid_headers off;

    location / {
        if ($uri ~ "^(.*)\.(jpg|jpeg|png|gif|ico|ttf|eot|svg|woff|woff2|css|js)$") {
                add_header 'Cache-Control' no-cache ;
        }
    aio threads=default ;

    proxy_set_header   Host               $host;
    proxy_set_header   X-Real-IP          $remote_addr;
    proxy_set_header   X-URIScheme           https;
        proxy_pass  http://localhost:4030/;
        set $cors '';
        if ($http_origin ~ '^https?://(localhost|onix\-gateway\.becknprotocol\.io)') {
                set $cors 'true';
        }
        add_header 'Access-Control-Allow-Origin' "$http_origin" always;

        if ($cors = 'true') {
                #add_header 'Access-Control-Allow-Origin' "$http_origin" always;
                add_header 'Access-Control-Allow-Credentials' 'true' always;
                add_header 'Access-Control-Allow-Methods' 'GET, POST, PUT, DELETE, OPTIONS' always;
                add_header 'Access-Control-Allow-Headers' 'Accept,Authorization,Cache-Control,Content-Type,DNT,If-Modified-Since,Keep-Alive,Origin,User-Agent,X-Requested-With,Range,ApiKey,pub_key_format' always;
        }

        if ($request_method = 'OPTIONS') {
                add_header 'Access-Control-Allow-Origin' "$http_origin" always;
                add_header 'Access-Control-Allow-Credentials' 'true' always;
                add_header 'Access-Control-Allow-Methods' 'GET, POST, PUT, DELETE, OPTIONS' always;
                add_header 'Access-Control-Allow-Headers' 'Accept,Authorization,Cache-Control,Content-Type,DNT,If-Modified-Since,Keep-Alive,Origin,User-Agent,X-Requested-With,Range,ApiKey,pub_key_format' always;
                # Tell client that this pre-flight info is valid for 20 days
                add_header 'Access-Control-Max-Age' 1728000;
                add_header 'Content-Type' 'text/plain charset=UTF-8';
                add_header 'Content-Length' 0;
                return 204;
        }
    }


    listen 443 ssl; # managed by Certbot
    ssl_certificate /etc/letsencrypt/live/onix-gateway.becknprotocol.io/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/onix-gateway.becknprotocol.io/privkey.pem;
    #include /etc/letsencrypt/options-ssl-nginx.conf;
    #ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;

}

server {
    if ($host = onix-gateway.becknprotocol.io) {
        return 301 https://$host$request_uri;
    }


     listen 80;
listen [::]:80;
server_name onix-gateway.becknprotocol.io;
    return 404;


}
```

## Nginx sample configuration for BAP

The BAP requires two URLs. One is for the BAP network (e.g https://onix-bap.becknprotocol.io') which faces the Beckn network. The other is for the BAP client 'https://onix-bap-client.becknprotocol.io' which faces the buyer side applications.

The following is a sample Nginx configuration for BAP client (e.g 'https://onix-bap-client.becknprotocol.io')

```
server {
        listen 80;
        listen [::]:80;
                # Put the server name as website name <website-name>.
        server_name onix-bap-client.becknprotocol.io;

                location / {
                        # This for Host, Client and Forwarded For
                        #proxy_set_header Host $http_host;
                        proxy_set_header X-Real-IP $remote_addr;
                        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

                        # For Web Sockets.
                        proxy_http_version 1.1;
                        proxy_set_header Upgrade $http_upgrade;
                        proxy_set_header Connection "upgrade";

                        # For Proxy.
                        proxy_pass "http://localhost:5001";
                }
}

server {
        listen 443 ssl http2;
        listen [::]:443 ssl http2;

                # Put the server name as website name <website-name>.
        server_name onix-bap-client.becknprotocol.io;

                # Point it to the port in which you want to run the server http://localhost:<Port-Number>.
                location / {
                        # This for Host, Client and Forwarded For
                        #proxy_set_header Host $http_host;
                        proxy_set_header X-Real-IP $remote_addr;
                        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

                        # For Web Sockets.
                        proxy_http_version 1.1;
                        proxy_set_header Upgrade $http_upgrade;
                        proxy_set_header Connection "upgrade";

                        # For Proxy.
                        proxy_pass "http://localhost:5001";
                }


                # This is the path to certificate. /etc/letsencrypt/live/<Domain-name>/fullchain.pem
        ssl_certificate /etc/letsencrypt/live/onix-bap-client.becknprotocol.io/fullchain.pem;


                # This is the path to certificate. /etc/letsencrypt/live/<Domain-name>/privkey.pem
        ssl_certificate_key /etc/letsencrypt/live/onix-bap-client.becknprotocol.io/privkey.pem;

                ssl_session_timeout 1d;
        ssl_session_cache shared:MozSSL:10m;  # about 40000 sessions
        ssl_session_tickets off;

        # curl https://ssl-config.mozilla.org/ffdhe2048.txt > /path/to/dhparam
        # ssl_dhparam /path/to/dhparam;

        # intermediate configuration
        ssl_protocols TLSv1.2 TLSv1.3;
        ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384;
        ssl_prefer_server_ciphers on;

        # HSTS (ngx_http_headers_module is required) (63072000 seconds)
        add_header Strict-Transport-Security "max-age=63072000" always;

        # OCSP stapling
        ssl_stapling on;
        ssl_stapling_verify on;

        # verify chain of trust of OCSP response using Root CA and Intermediate certs
        # ssl_trusted_certificate /path/to/root_CA_cert_plus_intermediates;

        # replace with the IP address of your resolver
        resolver 8.8.8.8;
}
```

The following is a sample Nginx configuration for BAP Network (e.g. 'https://onix-bap.becknprotocol.io')

```
server {
        listen 80;
        listen [::]:80;
                # Put the server name as website name <website-name>.
        server_name onix-bap.becknprotocol.io;

                location / {
                        # This for Host, Client and Forwarded For
                        #proxy_set_header Host $http_host;
                        proxy_set_header X-Real-IP $remote_addr;
                        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

                        # For Web Sockets.
                        proxy_http_version 1.1;
                        proxy_set_header Upgrade $http_upgrade;
                        proxy_set_header Connection "upgrade";

                        # For Proxy.
                        proxy_pass "http://localhost:5002";
                }
}

server {
        listen 443 ssl http2;
        listen [::]:443 ssl http2;

                # Put the server name as website name <website-name>.
        server_name onix-bap.becknprotocol.io;

                # Point it to the port in which you want to run the server http://localhost:<Port-Number>.
                location / {
                        # This for Host, Client and Forwarded For
                        #proxy_set_header Host $http_host;
                        proxy_set_header X-Real-IP $remote_addr;
                        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

                        # For Web Sockets.
                        proxy_http_version 1.1;
                        proxy_set_header Upgrade $http_upgrade;
                        proxy_set_header Connection "upgrade";

                        # For Proxy.
                        proxy_pass "http://localhost:5002";
                }


                # This is the path to certificate. /etc/letsencrypt/live/<Domain-name>/fullchain.pem
        ssl_certificate /etc/letsencrypt/live/onix-bap.becknprotocol.io/fullchain.pem;


                # This is the path to certificate. /etc/letsencrypt/live/<Domain-name>/privkey.pem
        ssl_certificate_key /etc/letsencrypt/live/onix-bap.becknprotocol.io/privkey.pem;

                ssl_session_timeout 1d;
        ssl_session_cache shared:MozSSL:10m;  # about 40000 sessions
        ssl_session_tickets off;

        # curl https://ssl-config.mozilla.org/ffdhe2048.txt > /path/to/dhparam
        # ssl_dhparam /path/to/dhparam;

        # intermediate configuration
        ssl_protocols TLSv1.2 TLSv1.3;
        ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384;
        ssl_prefer_server_ciphers on;

        # HSTS (ngx_http_headers_module is required) (63072000 seconds)
        add_header Strict-Transport-Security "max-age=63072000" always;

        # OCSP stapling
        ssl_stapling on;
        ssl_stapling_verify on;

        # verify chain of trust of OCSP response using Root CA and Intermediate certs
        # ssl_trusted_certificate /path/to/root_CA_cert_plus_intermediates;

        # replace with the IP address of your resolver
        resolver 8.8.8.8;
}
```

## Nginx sample configuration for BPP

The BPP requires two URLs. One is for the BPP Network 'https://onix-bpp.becknprotocol.io' which faces the Beckn network. The other is for the BPP client 'https://onix-bpp-client.becknprotocol.io' which faces the seller side applciation.

The following is the sample Nginx configuration for BPP client (e.g. 'https://onix-bpp-client.becknprotocol.io')

```
server {
        listen 80;
        listen [::]:80;
                # Put the server name as website name <website-name>.
        server_name onix-bpp-client.becknprotocol.io;

                location / {
                        # This for Host, Client and Forwarded For
                        proxy_set_header Host $http_host;
                        proxy_set_header X-Real-IP $remote_addr;
                        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

                        # For Web Sockets.
                        #proxy_http_version 1.1;
                        proxy_set_header Upgrade $http_upgrade;
                        proxy_set_header Connection "upgrade";

                        # For Proxy.
                        proxy_pass "http://localhost:6001";
                }
}

server {
        listen 443 ssl http2;
        listen [::]:443 ssl http2;

                # Put the server name as website name <website-name>.
        server_name onix-bpp-client.becknprotocol.io;

                # Point it to the port in which you want to run the server http://localhost:<Port-Number>.
                location / {
                        # This for Host, Client and Forwarded For
                        proxy_set_header Host $http_host;
                        proxy_set_header X-Real-IP $remote_addr;
                        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

                        # For Web Sockets.
                        #proxy_http_version 1.1;
                        proxy_set_header Upgrade $http_upgrade;
                        proxy_set_header Connection "upgrade";

                        # For Proxy.
                        proxy_pass "http://localhost:6001";
                }


                # This is the path to certificate. /etc/letsencrypt/live/<Domain-name>/fullchain.pem
        ssl_certificate /etc/letsencrypt/live/onix-bpp-client.becknprotocol.io/fullchain.pem;


                # This is the path to certificate. /etc/letsencrypt/live/<Domain-name>/privkey.pem
        ssl_certificate_key /etc/letsencrypt/live/onix-bpp-client.becknprotocol.io/privkey.pem;

        ssl_session_timeout 1d;
        ssl_session_cache shared:MozSSL:10m;  # about 40000 sessions
        ssl_session_tickets off;

        # curl https://ssl-config.mozilla.org/ffdhe2048.txt > /path/to/dhparam
        # ssl_dhparam /path/to/dhparam;

        # intermediate configuration
        ssl_protocols TLSv1.2 TLSv1.3;
        ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384;
        ssl_prefer_server_ciphers on;

        # HSTS (ngx_http_headers_module is required) (63072000 seconds)
        add_header Strict-Transport-Security "max-age=63072000" always;

        # OCSP stapling
        ssl_stapling on;
        ssl_stapling_verify on;

        # verify chain of trust of OCSP response using Root CA and Intermediate certs
        # ssl_trusted_certificate /path/to/root_CA_cert_plus_intermediates;

        # replace with the IP address of your resolver
        resolver 8.8.8.8;
}
```

The following is the sample Nginx configuration for BPP Network (e.g. 'https://onix-bpp.becknprotocol.io')

```
server {
        listen 80;
        listen [::]:80;
                # Put the server name as website name <website-name>.
        server_name onix-bpp.becknprotocol.io;

                location / {
                        # This for Host, Client and Forwarded For
                        proxy_set_header Host $http_host;
                        proxy_set_header X-Real-IP $remote_addr;
                        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

                        # For Web Sockets.
                        #proxy_http_version 1.1;
                        proxy_set_header Upgrade $http_upgrade;
                        proxy_set_header Connection "upgrade";

                        # For Proxy.
                        proxy_pass "http://localhost:6002";
                }
}

server {
        listen 443 ssl http2;
        listen [::]:443 ssl http2;

                # Put the server name as website name <website-name>.
        server_name onix-bpp.becknprotocol.io;

                # Point it to the port in which you want to run the server http://localhost:<Port-Number>.
                location / {
                        # This for Host, Client and Forwarded For
                        proxy_set_header Host $http_host;
                        proxy_set_header X-Real-IP $remote_addr;
                        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;

                        # For Web Sockets.
                        #proxy_http_version 1.1;
                        proxy_set_header Upgrade $http_upgrade;
                        proxy_set_header Connection "upgrade";

                        # For Proxy.
                        proxy_pass "http://localhost:6002";
                }


                # This is the path to certificate. /etc/letsencrypt/live/<Domain-name>/fullchain.pem
        ssl_certificate /etc/letsencrypt/live/onix-bpp.becknprotocol.io/fullchain.pem;


                # This is the path to certificate. /etc/letsencrypt/live/<Domain-name>/privkey.pem
        ssl_certificate_key /etc/letsencrypt/live/onix-bpp.becknprotocol.io/privkey.pem;

                ssl_session_timeout 1d;
        ssl_session_cache shared:MozSSL:10m;  # about 40000 sessions
        ssl_session_tickets off;

        # curl https://ssl-config.mozilla.org/ffdhe2048.txt > /path/to/dhparam
        # ssl_dhparam /path/to/dhparam;

        # intermediate configuration
        ssl_protocols TLSv1.2 TLSv1.3;
        ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:DHE-RSA-AES128-GCM-SHA256:DHE-RSA-AES256-GCM-SHA384;
        ssl_prefer_server_ciphers on;

        # HSTS (ngx_http_headers_module is required) (63072000 seconds)
        add_header Strict-Transport-Security "max-age=63072000" always;

        # OCSP stapling
        ssl_stapling on;
        ssl_stapling_verify on;

        # verify chain of trust of OCSP response using Root CA and Intermediate certs
        # ssl_trusted_certificate /path/to/root_CA_cert_plus_intermediates;

        # replace with the IP address of your resolver
        resolver 8.8.8.8;
}
```
