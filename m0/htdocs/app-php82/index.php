<?php
// M0 dummy - plain project pinned to PHP 8.2
if (isset($_GET['check'])) {
    header('Content-Type: text/plain');
    echo 'project=app-php82 php=' . PHP_VERSION . ' sapi=' . php_sapi_name();
    exit;
}
phpinfo();
