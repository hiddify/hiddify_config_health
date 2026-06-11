{% if TLS %}
"security": "tls",
"tlsSettings": {
  "alpn": ["h2", "http/1.1"],
  "certificates": [
    {
      "certificateFile": "{{TLS_CERT}}", 
      "keyFile": "{{TLS_KEY}}"
    }
  ]
},

{%endif%}