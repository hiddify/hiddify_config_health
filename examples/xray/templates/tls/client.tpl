{% if TLS %}
"security": "tls",
"tlsSettings": {
  "serverName": "{{SNI_NAME}}",
  "alpn": ["h2", "http/1.1"],
  "allowInsecure": false,
  "certificates": [
    {
      "certificateFile": "{{TLS_CA}}"
    }
  ]
},
{%endif%}