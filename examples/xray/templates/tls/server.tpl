{% if TLS %}
"security": "tls",
"tlsSettings": {
  "certificates": [
    {
      "certificateFile": "{{TLS_CERT}}", 
      "keyFile": "{{TLS_KEY}}"
    }
  ]
},

{%endif%}