<?php
// M0 dummy - plain project pinned to PHP 8.3
if (isset($_GET['check'])) {
    header('Content-Type: text/plain');
    echo 'project=app-php83 php=' . PHP_VERSION . ' sapi=' . php_sapi_name();
    exit;
}
phpinfo();
