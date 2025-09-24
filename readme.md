steps:
=====
1) git clone https://github.com/ANISH-GOTTAPU/FPVALIDATOR
2) Build binary 
    -- go build -o validator main.go helpers.go
3) Move the binary to /usr/local/bin
    -- sudo mv ./validator /usr/local/bin/validator
4) Run the validator against the file path
    -- validator <file-path>