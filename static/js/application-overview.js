$(function () {

    $(document).ready(function () {
        let status = getApplicationStatus();
        if (status !== STATUS.CREATED && status !== STATUS.FAILED) {
            setTimeout(function () {
                location.reload();
            }, delayTime);
        }
    });

});

let delayTime = 10000;

function getApplicationStatus() {
    return $('.status-info .status .card-body td.app-status').data('status').trim();
}